# Xiaomi MiMo WASM Extension (Rust)

WASM_OUT = dist/xiaomi-mimo.wasm

.PHONY: build clean

build:
	@mkdir -p dist
	cargo build --release --target wasm32-unknown-unknown
	cp target/wasm32-unknown-unknown/release/xiaomi_mimo.wasm $(WASM_OUT)
	@ls -lh $(WASM_OUT)

clean:
	rm -rf dist target
