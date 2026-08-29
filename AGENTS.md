# FlameGate Extensions — Agent Knowledge Base

**Generated:** 2026-08-30
**Module:** `github.com/bobbyunknown/flamegate/flamegate-ext`
**Stack:** Rust (`wasm32-unknown-unknown`) + TinyGo + Wazero WASM Runtime

---

## 🎯 PURPOSE & PHILOSOPHY

`flamegate-ext` contains all LLM Provider Extensions for FlameGate.

FlameGate core is a thin, generic gateway. **ALL LLM provider adapters, OAuth flows, and dialect transformers MUST be implemented as WASM Extensions in this directory**, never hardcoded in the Go gateway core.

---

## 📁 REPOSITORY STRUCTURE

```
flamegate-ext/
├── antigravity/             # Google Antigravity & CodeAssist extension (Rust)
│   ├── Cargo.toml
│   ├── Makefile
│   ├── schema.json          # Extension manifest & capability declaration
│   ├── src/lib.rs           # Extension implementation (invoke, list_models, OAuth)
│   └── dist/
│       └── antigravity.wasm # Compiled WASM artifact
├── cline/                   # ClinePass extension (TinyGo)
│   ├── go.mod
│   ├── Makefile
│   ├── schema.json
│   └── main.go
├── xiaomi-mimo/             # Xiaomi MiMo extension (Rust)
│   ├── Cargo.toml
│   ├── Makefile
│   ├── schema.json
│   └── src/lib.rs
├── publisher/               # Release & packaging tools (bundle, release)
└── store/                   # Registry metadata & extensions catalog
    └── index.json           # Extension store catalog
```

---

## 📄 MANIFEST SCHEMAS (`schema.json`)

Every extension directory MUST include a `schema.json` declaring its metadata and entrypoints.

### Example: OAuth Extension (`antigravity/schema.json`)
```json
{
  "slug": "antigravity",
  "name": "Antigravity",
  "version": "0.1.0",
  "description": "Google Antigravity & CodeAssist provider extension via WASM. Provides Gemini & Claude models with Google OAuth and v1internal integration.",
  "entrypoints": {
    "chat": "invoke",
    "models": "list_models"
  },
  "auth_modes": ["oauth"],
  "timeout": 120,
  "default_account_key": "default"
}
```

### Example: API Key Extension (`xiaomi-mimo/schema.json`)
```json
{
  "slug": "xiaomi-mimo",
  "name": "Xiaomi MiMo",
  "version": "0.1.0",
  "description": "Xiaomi MiMo AI integration (Rust) with pay-as-you-go API key auth.",
  "entrypoints": {
    "chat": "invoke",
    "models": "list_models"
  },
  "auth_modes": ["api_key"],
  "timeout": 120,
  "default_account_key": "default"
}
```

---

## 🏪 STORE REGISTRY SCHEMA (`store/index.json`)

The extensions catalog used by FlameGate to discover and install extensions:

```json
{
  "version": 1,
  "index_url": "https://raw.githubusercontent.com/bobbyunknown/flamegate-ext/main/store/index.json",
  "extensions": [
    {
      "slug": "cline",
      "name": "Cline",
      "description": "Cline extension via WASM (TinyGo) with WorkOS OAuth2 support.",
      "source": {
        "type": "github",
        "owner": "bobbyunknown",
        "repo": "flamegate-ext",
        "tag_prefix": "cline-v",
        "asset_pattern": "cline-{version}.zip"
      }
    },
    {
      "slug": "xiaomi-mimo",
      "name": "Xiaomi MiMo",
      "description": "Xiaomi MiMo AI integration (Rust) with pay-as-you-go API key auth.",
      "source": {
        "type": "github",
        "owner": "bobbyunknown",
        "repo": "flamegate-ext",
        "tag_prefix": "xiaomi-mimo-v",
        "asset_pattern": "xiaomi-mimo-{version}.zip"
      }
    },
    {
      "slug": "antigravity",
      "name": "Antigravity",
      "description": "Google Antigravity & CodeAssist provider extension (Rust) with Google OAuth2.",
      "source": {
        "type": "github",
        "owner": "bobbyunknown",
        "repo": "flamegate-ext",
        "tag_prefix": "antigravity-v",
        "asset_pattern": "antigravity-{version}.zip"
      }
    }
  ]
}
```

---

## 🔌 WASM EXTENSION CONTRACT & JSON PAYLOADS

### 1. Host Imports (from `"env"`)

```rust
#[link(wasm_import_module = "env")]
extern "C" {
    fn http_post(url_ptr: u32, url_len: u32, body_ptr: u32, body_len: u32, hdrs_ptr: u32, hdrs_len: u32) -> u32;
    fn http_get(url_ptr: u32, url_len: u32, hdrs_ptr: u32, hdrs_len: u32) -> u32;
    fn get_credentials(key_ptr: u32, key_len: u32) -> u32;
    fn emit_chunk(chunk_ptr: u32, chunk_len: u32);
}
```

---

### 2. Export: `list_models() -> u32`

Returns a pointer to a JSON array declaring models, tiers, and tags:

```json
[
  {
    "id": "gemini-3.7-flash-high",
    "name": "Gemini 3.7 Flash (High)",
    "tier": "flash",
    "tags": ["flash", "agent"]
  },
  {
    "id": "gemini-pro-agent",
    "name": "Gemini 3.1 Pro (High)",
    "tier": "pro",
    "tags": ["pro", "agent"]
  },
  {
    "id": "claude-sonnet-4-6",
    "name": "Claude Sonnet 4.6 (Thinking)",
    "tier": "frontier",
    "tags": ["frontier", "thinking"]
  },
  {
    "id": "gpt-oss-120b-medium",
    "name": "GPT-OSS 120B (Medium)",
    "tier": "free",
    "tags": ["free", "open-source"]
  }
]
```

#### Standard Tiers & Tags:
- **Tiers**: `"free"` | `"paid"` | `"pass"` | `"frontier"` | `"pro"` | `"flash"`
- **Tags**: Any descriptive keywords (e.g. `"thinking"`, `"agent"`, `"image"`, `"open-source"`). The frontend automatically renders filter pill buttons based on tags and tiers returned!

---

### 3. Export: `invoke(ptr: u32, len: u32) -> u32`

Handles both OAuth capabilities and LLM inference.

#### A. OAuth Capability: `oauth_authorize`
- **Input JSON**:
  ```json
  {
    "capability": "oauth_authorize",
    "redirect_uri": "http://localhost:20180/api/oauth/antigravity/callback",
    "state": "random-state-string"
  }
  ```
- **Output JSON**:
  ```json
  {
    "url": "https://accounts.google.com/o/oauth2/v2/auth?access_type=offline&prompt=consent&response_type=code&client_id=...&redirect_uri=...&scope=...&state=...",
    "state": "random-state-string"
  }
  ```

#### B. OAuth Capability: `oauth_exchange`
- **Input JSON**:
  ```json
  {
    "capability": "oauth_exchange",
    "code": "4/0AeanS0...",
    "redirect_uri": "http://localhost:20180/api/oauth/antigravity/callback"
  }
  ```
- **Output JSON**:
  ```json
  {
    "access_token": "ya29.a0AfH6...",
    "refresh_token": "1//04...",
    "expires_in": 3599,
    "email": "user@example.com",
    "account_name": "user@example.com",
    "project_id": "aicode-consumers"
  }
  ```

#### C. OAuth Capability: `oauth_refresh`
- **Input JSON**:
  ```json
  {
    "capability": "oauth_refresh",
    "refresh_token": "1//04..."
  }
  ```
- **Output JSON**:
  ```json
  {
    "access_token": "ya29.a0AfH6...",
    "refresh_token": "1//04...",
    "expires_in": 3599
  }
  ```

---

### 4. Inference Contract (Non-Streaming & Streaming)

#### Non-Streaming Inference
- **Input JSON** (OpenAI Chat format):
  ```json
  {
    "model": "gemini-2.5-flash",
    "stream": false,
    "messages": [
      {"role": "system", "content": "You are a helpful assistant."},
      {"role": "user", "content": "Hello!"}
    ],
    "temperature": 0.7
  }
  ```
- **Output JSON**:
  ```json
  {
    "content": "Hello! How can I help you today?",
    "finish_reason": "stop"
  }
  ```

#### Streaming Inference (`"stream": true`)
- Extension calls upstream streaming SSE endpoint.
- For each chunk received from upstream, extension parses the delta text and calls `emit_chunk(...)`:
  ```json
  {
    "choices": [
      {
        "index": 0,
        "delta": {
          "content": "Hello"
        }
      }
    ]
  }
  ```
- At the end of stream, extension emits:
  ```json
  {
    "choices": [
      {
        "index": 0,
        "delta": {
          "content": ""
        },
        "finish_reason": "stop"
      }
    ]
  }
  ```
- Function returns `0`.

---

## 🛠️ BUILD & LOCAL INSTALL COMMANDS

```bash
# 1. Build extension WASM artifact
cd antigravity && make build

# 2. Deploy to local FlameGate runtime directory (MUST include schema.json and <slug>.wasm)
mkdir -p ~/.flamegate/exts/antigravity
cp schema.json dist/antigravity.wasm ~/.flamegate/exts/antigravity/

# 3. Test bundle packaging via publisher tool
cd ../publisher && go run . bundle ../antigravity/
```
