# OpenAI Codex WASM Extension

WASM_OUT = dist/codex.wasm

.PHONY: build clean

build:
	@mkdir -p dist
	tinygo build -o $(WASM_OUT) -target wasm -no-debug -panic=trap .
	@ls -lh $(WASM_OUT)

clean:
	rm -rf dist
