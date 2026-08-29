# Cline WASM Extension (TinyGo)
# wasm-unknown: bare WASM importing only env.* host functions (no WASI/gojs).

WASM_OUT = dist/cline.wasm

.PHONY: build clean

build:
	@mkdir -p dist
	GOTOOLCHAIN=local tinygo build -o $(WASM_OUT) -target=wasm-unknown -no-debug -panic=trap .
	@ls -lh $(WASM_OUT)

clean:
	rm -rf dist
