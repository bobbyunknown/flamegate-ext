.PHONY: build test clean

build:
	cargo build --target wasm32-unknown-unknown --release
	mkdir -p dist
	cp target/wasm32-unknown-unknown/release/antigravity.wasm dist/antigravity.wasm

clean:
	rm -rf target dist
