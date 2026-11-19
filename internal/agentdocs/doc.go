// Package agentdocs turns the public tasks of a Runefile into guidance for
// an agent that has no MCP integration (spec 056): a section for AGENTS.md
// and a Claude skill.
//
// It builds one catalog from the analyzed Runefile, renders the same body
// for both formats, and plans the file changes from the existing bytes of
// the targets. It reads no file, writes no file, evaluates no expression,
// reads no environment and starts nothing: internal/cli does the reading
// and the writing.
package agentdocs
