// Command flamegate-publisher scaffolds, builds, bundles and signs FlameGate
// WASM extensions for publishing to GitHub Releases.
//
// Usage:
//
//	flamegate-publisher scaffold <slug>
//	flamegate-publisher build <slug>
//	flamegate-publisher bundle <slug> <version> [--out out.zip]
//	flamegate-publisher sign <file|SHA256SUMS> [--priv-hex ...] [--out SHA256SUMS.sig]
//	flamegate-publisher release <slug> <version> [--tag xiaomi-mimo-v0.1.3]
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "scaffold":
		if err := cmdScaffold(os.Args[2:]); err != nil {
			fail(err)
		}
	case "build":
		if err := cmdBuild(os.Args[2:]); err != nil {
			fail(err)
		}
	case "bundle":
		if err := cmdBundle(os.Args[2:]); err != nil {
			fail(err)
		}
	case "sign":
		if err := cmdSign(os.Args[2:]); err != nil {
			fail(err)
		}
	case "release":
		if err := cmdRelease(os.Args[2:]); err != nil {
			fail(err)
		}
	case "help", "-h", "--help":
		usage(os.Stdout)
	default:
		usage(os.Stderr)
		os.Exit(2)
	}
}

func fail(err error) {
	_, _ = fmt.Fprintln(os.Stderr, "flamegate-publisher:", err)
	os.Exit(1)
}

func usage(w *os.File) {
	_, _ = fmt.Fprint(w, `Usage: flamegate-publisher <command> [flags]

Commands:
  scaffold <slug>             Create a new extension directory template
  build <slug>                Build the WASM binary using the extension's Makefile
  bundle <slug> <version>     Package schema.json + <slug>.wasm + SHA256SUMS (+ .sig) into a zip
  sign <file>                 Sign a SHA256SUMS file with a private key (32-byte hex seed)
  release <slug> <version>    Create a tag + GitHub release and upload the bundle
  help                        Show this help
`)
}
