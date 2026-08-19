# FlameGate WASM Extensions

Official WebAssembly (WASM) extension modules for [FlameGate](https://github.com/bobbyunknown/flamegate) LLM Proxy & Router.

Extensions allow FlameGate to interface with specialized LLM providers, custom API dialects, and proprietary authentication mechanisms without modifying core gateway binaries.

---

## 📦 Available Extensions

| Extension | Language | Description | Auth Mode |
|---|---|---|---|
| [`xiaomi-mimo`](./xiaomi-mimo/) | Rust | Xiaomi MiMo AI integration | API Key |

---

## 🚀 Quick Start

### Prerequisites

- **Go extensions**: [TinyGo](https://tinygo.org/) v0.30+
- **Rust extensions**: `rustup target add wasm32-unknown-unknown`
- **Build tool**: `make`

### Building an Extension

To build a specific extension into a WebAssembly binary:

#### 1. Build Xiaomi MiMo (Rust)

```bash
cd xiaomi-mimo
make build
```

The compiled WebAssembly artifact will be generated at `xiaomi-mimo/dist/xiaomi-mimo.wasm`.

---

## 🛠️ Extension Structure

Every extension directory contains a manifest file (`schema.json`) and the WebAssembly module:

```
flamegate-ext/
└── xiaomi-mimo/
    ├── schema.json
    ├── Cargo.toml
    ├── src/lib.rs           # Rust WASM source code
    ├── Makefile
    └── dist/
        └── xiaomi-mimo.wasm
```

### Manifest Schema (`schema.json`)

The `schema.json` file declares the extension capabilities and entrypoint functions exported by the WASM module:

```json
{
  "slug": "xiaomi-mimo",
  "name": "Xiaomi MiMo",
  "version": "0.1.0",
  "description": "Xiaomi MiMo extension via WASM (Rust). Pay-as-you-go API key auth.",
  "entrypoints": {
    "chat": "invoke",
    "models": "list_models"
  },
  "timeout": 120,
  "default_account_key": "default"
}
```

> [!NOTE]
> Extensions run inside FlameGate's sandboxed WASM runtime. Network calls and state management are routed through FlameGate host imports.

---

## 🔧 Developing New Extensions

You can write extensions in any language that targets WebAssembly (`wasm32`).

1. Define the extension manifest in `schema.json`.
2. Export the required entrypoint functions (`chat`, `models`).
3. Compile the code into a `.wasm` binary under `dist/`.
4. Register the extension directory in FlameGate's runtime path.
