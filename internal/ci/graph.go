package ci

import (
	"errors"
	"slices"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/diag"
	"github.com/rune-task-runner/rune/internal/eval"
	"github.com/rune-task-runner/rune/internal/runtime/scheduler"
)

// Resolver binds tasks as the scheduler's engine does, for one target OS.
type Resolver interface {
	// BindRoot binds the arguments of a root task, as `rune TASK ARG...`
	// binds them. It refuses a root that is not available on the OS.
	BindRoot(t *ast.Task, args []string) (eval.Args, error)
	// ResolveDep evaluates a dependency or post-hook call in the scope of
	// the calling task, as scheduler.Engine.ResolveDep does.
	ResolveDep(cur *ast.Task, args eval.Args, dep *ast.DepCall) (*ast.Task, eval.Args, error)
	// Available reports whether a task runs on the OS.
	Available(t *ast.Task) bool
}

// Root is a task named on the command line, with its arguments as typed.
type Root struct {
	Task *ast.Task
	Args []string
}

// Node is one task with bound arguments: what the scheduler runs once, and
// what one job runs.
type Node struct {
	Task *ast.Task
	Args eval.Args
	// Argv are the words that bind Args on a command line: every parameter
	// in order, the elements of a variadic one as separate words.
	Argv []string
	// Key is the scheduler's run-once key of the node.
	Key string
	// Needs are the nodes whose jobs finish before this one starts, in
	// Index order, without a node another need already implies.
	Needs []*Node
	// Done is this node and what its post-hooks need to be done: what a
	// task that depends on it waits for.
	Done []*Node
	// Index is the position of the node in the graph, a topological order.
	Index int
	// Cached is true when the task has [cache]; Outputs when that [cache]
	// declares outputs, which later jobs receive.
	Cached, Outputs bool
}

// Graph is the nodes of one OS in Index order, and the root nodes.
type Graph struct {
	OS    string // GOOS of the runners: "linux" or "darwin"
	Nodes []*Node
	Roots []*Node
}

// Input is what Build needs. Tasks and Assigns are the Runefile's, after
// imports and modules: Build reads them to check an expression before the
// Resolver evaluates it.
type Input struct {
	OS       string
	Roots    []Root
	Resolver Resolver
	Tasks    map[string]*ast.Task
	Assigns  map[string]*ast.Assignment
}

// Refusal is the error of a graph that a workflow cannot run faithfully or
// safely. Diags hold one diagnostic per reason, at the source it is about.
type Refusal struct {
	Diags diag.List
}

func (r *Refusal) Error() string {
	if len(r.Diags) == 1 {
		return r.Diags[0].Message
	}
	return "the tasks cannot run as a GitHub Actions workflow"
}

// Build walks the roots as scheduler.Run would walk them, without running
// anything, and returns the graph of their nodes. A dependency without
// [parallel] waits for the dependencies listed before it, a post-hook waits
// for its task, and a task's dependents wait for its post-hooks. Roots do
// not wait for each other. Every refusal of the whole graph is returned in
// one *Refusal.
func Build(in Input) (*Graph, error) {
	b := &builder{in: in, byKey: map[string]*Node{}}
	g := &Graph{OS: in.OS}
	for _, r := range in.Roots {
		args, ok, err := b.bindRoot(r)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if _, err := b.visit(r.Task, args, nil, nil); err != nil {
			return nil, err
		}
		if n := b.byKey[nodeKey(r.Task, args)]; !slices.Contains(g.Roots, n) {
			g.Roots = append(g.Roots, n)
		}
	}
	g.Nodes = b.nodes
	b.checkBackground()
	if len(b.diags) > 0 {
		return nil, &Refusal{Diags: b.diags}
	}
	reduceNeeds(g.Nodes)
	return g, nil
}

type builder struct {
	in    Input
	nodes []*Node
	byKey map[string]*Node
	diags diag.List
}

func nodeKey(t *ast.Task, args eval.Args) string { return t.Name + "(" + args.Key() + ")" }

// bindRoot checks and binds a root. A root the checks refuse is left out,
// with its diagnostics recorded; another error of the bind is returned.
func (b *builder) bindRoot(r Root) (eval.Args, bool, error) {
	if !b.pureDefaults(r.Task, len(r.Args)) {
		return eval.Args{}, false, nil
	}
	args, err := b.in.Resolver.BindRoot(r.Task, r.Args)
	var refusal *Refusal
	if errors.As(err, &refusal) {
		b.diags = append(b.diags, refusal.Diags...)
		return eval.Args{}, false, nil
	}
	return args, err == nil, err
}

// visit returns the done set of a node, creating its job the first time.
// after is what its job, and its dependencies' jobs, must wait for.
func (b *builder) visit(t *ast.Task, args eval.Args, after []*Node, chain []string) ([]*Node, error) {
	if slices.Contains(chain, t.Name) {
		return nil, &scheduler.CycleError{Path: cyclePath(chain, t.Name)}
	}
	key := nodeKey(t, args)
	if n, ok := b.byKey[key]; ok {
		return n.Done, nil
	}
	n := &Node{Task: t, Args: args, Argv: Words(t, args), Key: key}
	if c := t.Attr(ast.AttrCache); c != nil {
		n.Cached = true
		n.Outputs = len(c.Outputs) > 0
	}
	b.byKey[key] = n
	b.checkNode(n)
	chain = append(slices.Clip(chain), t.Name)

	needs := after
	for _, dep := range t.Deps {
		depAfter := needs
		if t.Attr(ast.AttrParallel) != nil {
			depAfter = after
		}
		done, err := b.dep(t, args, dep, depAfter, chain)
		if err != nil {
			return nil, err
		}
		needs = union(needs, done)
	}
	n.Needs = needs
	n.Index = len(b.nodes)
	b.nodes = append(b.nodes, n)

	done := []*Node{n}
	for _, hook := range t.PostHooks {
		d, err := b.dep(t, args, hook, done, chain)
		if err != nil {
			return nil, err
		}
		done = union(done, d)
	}
	n.Done = done
	return done, nil
}

// dep resolves one dependency or post-hook call and visits its target. A
// target not available on the OS adds nothing, as the scheduler skips it.
func (b *builder) dep(cur *ast.Task, args eval.Args, call *ast.DepCall, after []*Node, chain []string) ([]*Node, error) {
	if target := b.in.Tasks[call.Name]; target != nil {
		if !b.in.Resolver.Available(target) {
			return nil, nil
		}
		if !b.pureCall(cur, call, target) {
			return nil, nil
		}
	}
	target, targetArgs, err := b.in.Resolver.ResolveDep(cur, args, call)
	if err != nil {
		return nil, err
	}
	if !b.in.Resolver.Available(target) {
		return nil, nil
	}
	// The job binds the arguments again from its command line. A call can
	// bind what a command line cannot, such as an empty list forwarded into
	// a + parameter, and that job would fail where the local run does not.
	if _, err := b.in.Resolver.BindRoot(target, Words(target, targetArgs)); err != nil {
		b.refuse(call.Sp, "task %q gets arguments from this call that its job could not pass on a command line (%v); pass the arguments the task needs", target.Name, err)
		return nil, nil
	}
	return b.visit(target, targetArgs, after, chain)
}

// Words lists the words that bind args to t's parameters on a command line:
// every parameter in order, the elements of a variadic one as separate words.
func Words(t *ast.Task, args eval.Args) []string {
	var words []string
	for _, p := range t.Params {
		if p.Kind == ast.ParamVariadicPlus || p.Kind == ast.ParamVariadicStar {
			words = append(words, args.Lists[p.Name]...)
			continue
		}
		words = append(words, args.Values[p.Name])
	}
	return words
}

// union returns a with the nodes of b it lacks, in Index order.
func union(a, b []*Node) []*Node {
	out := slices.Clone(a)
	for _, n := range b {
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	slices.SortFunc(out, func(x, y *Node) int { return x.Index - y.Index })
	return out
}

// reduceNeeds drops from each node's needs a node that another of its needs
// already waits for, directly or not.
func reduceNeeds(nodes []*Node) {
	ancestors := make(map[*Node]map[*Node]bool, len(nodes))
	for _, n := range nodes {
		anc := map[*Node]bool{}
		for _, m := range n.Needs {
			anc[m] = true
			for a := range ancestors[m] {
				anc[a] = true
			}
		}
		ancestors[n] = anc
	}
	for _, n := range nodes {
		n.Needs = slices.DeleteFunc(slices.Clone(n.Needs), func(m *Node) bool {
			return slices.ContainsFunc(n.Needs, func(o *Node) bool { return o != m && ancestors[o][m] })
		})
	}
}

// Ancestors returns every node n waits for, directly or not, in Index order.
func (n *Node) Ancestors() []*Node {
	seen := map[*Node]bool{}
	var walk func(*Node)
	walk = func(x *Node) {
		for _, m := range x.Needs {
			if !seen[m] {
				seen[m] = true
				walk(m)
			}
		}
	}
	walk(n)
	out := make([]*Node, 0, len(seen))
	for m := range seen {
		out = append(out, m)
	}
	slices.SortFunc(out, func(x, y *Node) int { return x.Index - y.Index })
	return out
}

// cyclePath returns the cycle starting where back first appears in chain,
// as the scheduler reports it.
func cyclePath(chain []string, back string) []string {
	start := slices.Index(chain, back)
	return append(slices.Clone(chain[start:]), back)
}
