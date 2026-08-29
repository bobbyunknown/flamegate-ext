package main

import (
	"archive/zip"
	"bufio"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const scaffoldTemplate = `{
  "slug": "%s",
  "name": "%s",
  "version": "0.1.0",
  "description": "%s extension via WASM.",
  "entrypoints": {
    "chat": "invoke",
    "models": "list_models"
  },
  "timeout": 120,
  "default_account_key": "default"
}
`

// cmdScaffold creates <slug>/schema.json + <slug>/Makefile.
func cmdScaffold(args []string) error {
	if len(args) < 1 {
		return errors.New("scaffold requires <slug>")
	}
	slug := args[0]
	if err := os.MkdirAll(slug, 0o755); err != nil {
		return err
	}
	schema := fmt.Sprintf(scaffoldTemplate, slug, strings.Title(slug), slug)
	if err := os.WriteFile(filepath.Join(slug, "schema.json"), []byte(schema), 0o644); err != nil {
		return err
	}
	makefile := fmt.Sprintf(`.PHONY: build dist/%s.wasm

build: dist/%s.wasm

dist/%s.wasm:
	mkdir -p dist
	# Replace with the real toolchain (tinygo build / cargo build --target wasm32-unknown-unknown etc.).
	cp main.wasm dist/%s.wasm 2>/dev/null || touch dist/%s.wasm
`, slug, slug, slug, slug, slug)
	if err := os.WriteFile(filepath.Join(slug, "Makefile"), []byte(makefile), 0o644); err != nil {
		return err
	}
	fmt.Printf("Scaffolded extension in %s/\n", slug)
	return nil
}

// cmdBuild runs `make build` in the extension directory.
func cmdBuild(args []string) error {
	if len(args) < 1 {
		return errors.New("build requires <slug>")
	}
	slug := args[0]
	return runCmd("make", "-C", slug, "build")
}

// cmdBundle assembles <slug>-<version>.zip with schema.json, wasm, SHA256SUMS,
// and SHA256SUMS.sig when present. Uses a system temp file for the manifest so
// it is never left inside the extension directory.
func cmdBundle(args []string) error {
	if len(args) < 2 {
		return errors.New("bundle requires <slug> <version>")
	}
	slug, version := args[0], args[1]

	wasmPath := filepath.Join(slug, "dist", slug+".wasm")
	if _, err := os.Stat(wasmPath); err != nil {
		matches, _ := filepath.Glob(filepath.Join(slug, "*.wasm"))
		if len(matches) > 0 {
			wasmPath = matches[0]
		} else {
			return fmt.Errorf("missing built wasm (run `flamegate-publisher build %s`)", slug)
		}
	}
	schemaPath := filepath.Join(slug, "schema.json")

	outZip := fmt.Sprintf("%s-%s.zip", slug, version)
	if o := flagValue(args, "--out"); o != "" {
		outZip = o
	}

	// Build the checksum manifest over the in-archive names.
	var b strings.Builder
	manifestEntries := []struct{ archiveName, abs string }{
		{"schema.json", schemaPath},
		{slug + ".wasm", wasmPath},
	}
	for _, e := range manifestEntries {
		data, err := os.ReadFile(e.abs)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		fmt.Fprintf(&b, "%s  %s\n", hex.EncodeToString(sum[:]), e.archiveName)
	}

	sumsTmp, err := os.CreateTemp("", "flamegate-sums-*")
	if err != nil {
		return err
	}
	defer os.Remove(sumsTmp.Name())
	if _, err := io.WriteString(sumsTmp, b.String()); err != nil {
		sumsTmp.Close()
		return err
	}
	sumsTmp.Close()

	zipFiles := []struct{ archiveName, abs string }{
		{"schema.json", schemaPath},
		{slug + ".wasm", wasmPath},
		{"SHA256SUMS", sumsTmp.Name()},
	}
	// Sign the manifest NOW so the signature covers the exact bytes in the zip.
	sigTmp, err := signFile(sumsTmp.Name(), flagValue(args, "--priv-hex"), flagValue(args, "--priv-env"))
	if err == nil && sigTmp != "" {
		defer os.Remove(sigTmp)
		zipFiles = append(zipFiles, struct{ archiveName, abs string }{"SHA256SUMS.sig", sigTmp})
	} else if err != nil {
		// absent key is fine for unsigned community bundles
		if !errors.Is(err, errNoKey) {
			return err
		}
	}

	f, err := os.Create(outZip)
	if err != nil {
		return err
	}
	defer f.Close()
	w := zip.NewWriter(f)
	for _, e := range zipFiles {
		hw, err := w.Create(e.archiveName)
		if err != nil {
			return err
		}
		rf, err := os.Open(e.abs)
		if err != nil {
			return err
		}
		_, cerr := io.Copy(hw, rf)
		rf.Close()
		if cerr != nil {
			return cerr
		}
	}
	if err := w.Close(); err != nil {
		return err
	}
	fmt.Printf("Wrote %s\n", outZip)
	return nil
}

func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// arg helpers ----------------------------------------------------------------

func flagValue(args []string, key string) string {
	for i, a := range args {
		if a == key && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// sha256sumsFile reports the hex SHA256 of a file.
func sha256sumsFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// readSHA256SUMSFile parses "<hex>  <file>" lines (kept for verification parity).
func readSHA256SUMSFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		out[fields[1]] = strings.ToLower(fields[0])
	}
	return out, sc.Err()
}

// errNoKey marks "no signing key available" (is not a hard failure).
var errNoKey = errors.New("no signing key")

// signFile writes a detached Ed25519 signature for target into a temp file and
// returns its path. If no key is available it returns errNoKey.
func signFile(target, privHex, envKey string) (string, error) {
	if privHex == "" && envKey != "" {
		privHex = os.Getenv(envKey)
	}
	if privHex == "" {
		privHex = os.Getenv("FLAMEGATE_SIGNING_KEY")
	}
	if privHex == "" {
		return "", errNoKey
	}
	seed, err := hex.DecodeString(strings.TrimSpace(privHex))
	if err != nil || len(seed) != ed25519.SeedSize {
		return "", errors.New("private key must be a 32-byte hex seed")
	}
	priv := ed25519.NewKeyFromSeed(seed)
	data, err := os.ReadFile(target)
	if err != nil {
		return "", err
	}
	sig := ed25519.Sign(priv, data)
	tmp, err := os.CreateTemp("", "flamegate-sig-*")
	if err != nil {
		return "", err
	}
	if _, err := tmp.Write(sig); err != nil {
		tmp.Close()
		return "", err
	}
	tmp.Close()
	return tmp.Name(), nil
}

// cmdSign implements the standalone sign subcommand.
func cmdSign(args []string) error {
	if len(args) < 1 {
		return errors.New("sign requires <file>")
	}
	target := args[0]
	out := flagValue(args, "--out")
	sigPath, err := signFile(target, flagValue(args, "--priv-hex"), flagValue(args, "--priv-env"))
	if err != nil {
		return err
	}
	defer os.Remove(sigPath)
	if out == "" {
		out = target + ".sig"
	}
	if err := copyFile(sigPath, out); err != nil {
		return err
	}
	fmt.Printf("Wrote raw 64-byte signature to %s\n", out)
	return nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}