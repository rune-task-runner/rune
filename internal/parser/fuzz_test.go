package parser

import "testing"

// FuzzParser asserts the parser never panics on arbitrary input and always
// returns a non-nil File.
func FuzzParser(f *testing.F) {
	seeds := []string{
		"",
		"set default := \"greet\"\n",
		"greet name=\"world\":\n    @echo hi {{name}}\n",
		"build: greet\n    echo build\n",
		"[cache(inputs=[\"a\"], outputs=[\"b\"])]\nx:\n    echo x\n",
		"a: (b \"arg\") c && d\n    echo a\n",
		"a: b && c || (d \"verbose\")\n    echo a\n",
		"a: || b\n    echo a\n",
		"x := if a == \"1\" { \"y\" } else { \"z\" }\n",
		"deploy: docker::push\n    echo done\n",
		"mod sub \"sub.rune\"\nimport? \"opt.rune\"\n",
		// Typed parameter annotations (spec 023): every kind, adjacency edges,
		// malformed enum lists, and the legacy unspaced-header hint path.
		"deploy env:enum(\"staging\",\"prod\") replicas:number=\"2\":\n    echo {{env}}\n",
		"test +packages:path:\n    echo {{packages}}\n",
		"lint *paths:string:\n    echo {{paths}}\n",
		"deploy env: build\n    echo hi\n",
		"deploy env:build\n    echo hi\n",
		"deploy env:enum():\n    echo hi\n",
		"deploy env:enum(x):\n    echo hi\n",
		"deploy env:number(\"1\")=\"2\":\n    echo hi\n",
		"deploy env:enum(\"a\",:\n    echo hi\n",
		// Regression: an unparseable token inside a dependency-call argument list
		// must not loop forever (parsePrimary now consumes it) — was an OOM.
		"a:(o i {{e}}",
		"a:(b {\n",
		"x := (}\n",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		file, _ := Parse("fuzz", src)
		if file == nil {
			t.Fatal("Parse returned nil File")
		}
	})
}
