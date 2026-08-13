// FlameGate WASM Extension — Xiaomi MiMo (Rust)
// Upstream: https://api.xiaomimimo.com/v1

// Host function imports — all pointers are u32 offsets into WASM linear memory
#[link(wasm_import_module = "env")]
extern "C" {
    fn http_post(
        url_ptr: u32, url_len: u32,
        body_ptr: u32, body_len: u32,
        hdrs_ptr: u32, hdrs_len: u32,
    ) -> u32;
    /// GET without body — used by list_models against OpenAI-compatible /models.
    fn http_get(
        url_ptr: u32, url_len: u32,
        hdrs_ptr: u32, hdrs_len: u32,
    ) -> u32;
    fn get_credentials(key_ptr: u32, key_len: u32) -> u32;
    fn emit_chunk(chunk_ptr: u32, chunk_len: u32);
}

static mut WRITE_BUF: [u8; 256 * 1024] = [0u8; 256 * 1024];
static mut WRITE_POS: u32 = 0;

/// Return absolute WASM linear memory address of WRITE_BUF start.
fn buf_base() -> u32 {
    unsafe { WRITE_BUF.as_ptr() as u32 }
}

/// Convert absolute WASM address to offset into WRITE_BUF.
fn to_offset(ptr: u32) -> usize {
    (ptr - buf_base()) as usize
}

/// Read bytes from guest memory at absolute address.
fn mem_slice(ptr: u32, len: u32) -> &'static [u8] {
    let off = to_offset(ptr);
    let l = len as usize;
    if off + l > unsafe { WRITE_BUF.len() } {
        return &[];
    }
    unsafe { &WRITE_BUF[off..off + l] }
}

/// Read host-written length-prefixed JSON at absolute WASM linear memory address.
/// Host ABI: [4 bytes LE length][JSON bytes].
/// Host writes directly to WASM linear memory via wazero, not through WRITE_BUF.
fn read_host_json(ptr: u32) -> &'static [u8] {
    unsafe {
        let len_bytes = core::slice::from_raw_parts(ptr as *const u8, 4);
        let len = u32::from_le_bytes([len_bytes[0], len_bytes[1], len_bytes[2], len_bytes[3]]);
        core::slice::from_raw_parts((ptr + 4) as *const u8, len as usize)
    }
}


/// Write string into WRITE_BUF at current WRITE_POS.
/// Returns (absolute_addr, len).
fn str_to_ptr(s: &str) -> (u32, u32) {
    let bytes = s.as_bytes();
    let pos = unsafe { WRITE_POS } as usize;
    if pos + bytes.len() > unsafe { WRITE_BUF.len() } {
        return (0, 0);
    }
    unsafe {
        WRITE_BUF[pos..pos + bytes.len()].copy_from_slice(bytes);
        WRITE_POS += bytes.len() as u32;
    }
    (buf_base() + pos as u32, bytes.len() as u32)
}

/// alloc: called by host to write data into guest memory.
/// Returns absolute WASM linear memory address.
#[no_mangle]
pub extern "C" fn alloc(size: u32) -> u32 {
    let pos = unsafe { WRITE_POS };
    if size == 0 || pos + size > unsafe { WRITE_BUF.len() as u32 } {
        return 0;
    }
    unsafe { WRITE_POS += size; }
    buf_base() + pos
}

#[no_mangle]
pub extern "C" fn dealloc(_ptr: u32, _size: u32) {}

#[no_mangle]
pub extern "C" fn reset_mem() {
    unsafe { WRITE_POS = 0; }
}

/// Write a length-prefixed JSON response into guest memory.
/// Returns absolute address of [4-byte LE length][JSON bytes].
fn write_json(s: &str) -> u32 {
    let bytes = s.as_bytes();
    let pos = unsafe { WRITE_POS } as usize;
    if pos + bytes.len() + 4 > unsafe { WRITE_BUF.len() } {
        return 0;
    }
    let len = bytes.len() as u32;
    unsafe {
        WRITE_BUF[pos] = len as u8;
        WRITE_BUF[pos + 1] = (len >> 8) as u8;
        WRITE_BUF[pos + 2] = (len >> 16) as u8;
        WRITE_BUF[pos + 3] = (len >> 24) as u8;
        WRITE_BUF[pos + 4..pos + 4 + bytes.len()].copy_from_slice(bytes);
        WRITE_POS += (4 + bytes.len()) as u32;
    }
    buf_base() + pos as u32
}

fn emit(b: &[u8]) {
    let (ptr, size) = str_to_ptr(core::str::from_utf8(b).unwrap_or(""));
    if size > 0 {
        unsafe { emit_chunk(ptr, size); }
    }
}

fn get_credential(key: &str) -> String {
    let (kptr, klen) = str_to_ptr(key);
    let ptr = unsafe { get_credentials(kptr, klen) };
    if ptr == 0 {
        return String::new();
    }
    // Host ABI: [4-byte LE length][JSON] at absolute linear-memory address.
    let raw = read_host_json(ptr);
    let s = core::str::from_utf8(raw).unwrap_or("");
    if let Some(v) = extract_json_string(s, "api_key") {
        if !v.is_empty() {
            return v;
        }
    }
    if let Some(v) = extract_json_string(s, "access_token") {
        if !v.is_empty() {
            return v;
        }
    }
    String::new()
}

fn split_lines(data: &[u8]) -> Vec<&[u8]> {
    let mut lines = Vec::new();
    let mut start = 0;
    for i in 0..data.len() {
        if data[i] == b'\n' {
            lines.push(&data[start..i]);
            start = i + 1;
        }
    }
    if start < data.len() {
        lines.push(&data[start..]);
    }
    lines
}

fn bytes_starts_with(data: &[u8], prefix: &[u8]) -> bool {
    data.len() >= prefix.len() && &data[..prefix.len()] == prefix
}

fn extract_json_string(json: &str, key: &str) -> Option<String> {
    let pattern = format!("\"{}\":\"", key);
    let start = json.find(&pattern)? + pattern.len();
    let end = json[start..].find('"')?;
    Some(json[start..start + end].to_string())
}

fn extract_json_array(json: &str, key: &str) -> Option<String> {
    let pattern = format!("\"{}\":[", key);
    let start = json.find(&pattern)? + pattern.len() - 1;
    let mut depth = 0;
    let mut end = start;
    for (i, ch) in json[start..].char_indices() {
        match ch {
            '[' => depth += 1,
            ']' => {
                depth -= 1;
                if depth == 0 {
                    end = start + i + 1;
                    break;
                }
            }
            _ => {}
        }
    }
    Some(json[start..end].to_string())
}

fn extract_delta_content(json: &str) -> Option<String> {
    let delta_pos = json.find("\"delta\":")?;
    let after_delta = &json[delta_pos..];
    let pat = "\"content\":\"";
    let content_pos = after_delta.find(pat)?;
    let val_start = content_pos + pat.len();
    let end = after_delta[val_start..].find('"')?;
    Some(after_delta[val_start..val_start + end].to_string())
}

fn extract_finish_reason(json: &str) -> Option<String> {
    let pat = "\"finish_reason\":\"";
    let pos = json.find(pat)?;
    let val_start = pos + pat.len();
    let end = json[val_start..].find('"')?;
    let val = &json[val_start..val_start + end];
    if val == "null" || val.is_empty() {
        return None;
    }
    Some(val.to_string())
}

/// Escape a string for safe inclusion inside a JSON string value.
fn json_escape(s: &str) -> String {
    let mut out = String::with_capacity(s.len() + 8);
    for ch in s.chars() {
        match ch {
            '"' => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            '\n' => out.push_str("\\n"),
            '\r' => out.push_str("\\r"),
            '\t' => out.push_str("\\t"),
            '\x08' => out.push_str("\\b"),
            '\x0C' => out.push_str("\\f"),
            c if c < '\x20' => out.push_str(&format!("\\u{:04x}", c as u32)),
            c => out.push(c),
        }
    }
    out
}

#[no_mangle]
pub extern "C" fn invoke(req_ptr: u32, _req_len: u32) -> u32 {
    unsafe { WRITE_POS = 0; }
    let req_bytes = read_host_json(req_ptr);
    let req_str = match core::str::from_utf8(req_bytes) {
        Ok(s) => s,
        Err(_) => return write_json("{\"error\":\"invalid request\"}"),
    };

    let model = extract_json_string(req_str, "model").unwrap_or_default();
    let stream = req_str.contains("\"stream\":true") || req_str.contains("\"stream\": true");
    let messages_json = extract_json_array(req_str, "messages").unwrap_or_else(|| "[]".to_string());

    let api_key = get_credential("api_key");

    let body = format!(
        "{{\"model\":\"{}\",\"stream\":{},\"messages\":{}}}",
        model, stream, messages_json
    );

    let (bptr, blen) = str_to_ptr(&body);
    let url = "https://api.xiaomimimo.com/v1/chat/completions";
    let (uptr, ulen) = str_to_ptr(url);
    let hdrs = format!(
        "{{\"Content-Type\":\"application/json\",\"Authorization\":\"Bearer {}\"}}",
        json_escape(&api_key)
    );
    let (hptr, hlen) = str_to_ptr(&hdrs);

    let resp_ptr = unsafe { http_post(uptr, ulen, bptr, blen, hptr, hlen) };
    if resp_ptr == 0 {
        return write_json("{\"error\":\"upstream failed\"}");
    }
    let resp_bytes = read_host_json(resp_ptr);

    if stream {
        for line in split_lines(resp_bytes) {
            if !bytes_starts_with(line, b"data: ") {
                continue;
            }
            let data = &line[6..];
            if data == b"[DONE]" {
                break;
            }
            let data_str = core::str::from_utf8(data).unwrap_or("");

            let content = extract_delta_content(data_str).unwrap_or_default();
            let finish = extract_finish_reason(data_str).unwrap_or_default();

            let chunk = if !finish.is_empty() {
                format!(
                    "{{\"type\":\"chunk\",\"choices\":[{{\"delta\":{{\"content\":\"{}\"}},\"index\":0,\"finish_reason\":\"{}\"}}]}}",
                    content, finish
                )
            } else {
                format!(
                    "{{\"type\":\"chunk\",\"choices\":[{{\"delta\":{{\"content\":\"{}\"}},\"index\":0}}]}}",
                    content
                )
            };
            emit(chunk.as_bytes());
        }
        emit(b"{\"type\":\"done\"}");
        return 0;
    }

    // Non-stream: extract content from choices[0].message.content
    let resp_str = core::str::from_utf8(resp_bytes).unwrap_or("{}");
    let content = resp_str
        .find("\"message\":{")
        .and_then(|msg_pos| {
            let msg_obj = &resp_str[msg_pos + 10..];
            // Find "content":" inside message object
            let pat = "\"content\":\"";
            let cpos = msg_obj.find(pat)?;
            let val_start = cpos + pat.len();
            // Handle escaped content properly
            let mut end = val_start;
            let mut esc = false;
            for ch in msg_obj[val_start..].chars() {
                if esc { esc = false; end += ch.len_utf8(); continue; }
                if ch == '\\' { esc = true; end += ch.len_utf8(); continue; }
                if ch == '"' { break; }
                end += ch.len_utf8();
            }
            Some(msg_obj[val_start..end].to_string())
        })
        .unwrap_or_default();
    let finish = extract_finish_reason(resp_str).unwrap_or_else(|| "stop".to_string());
    // content is already a valid JSON string value (escaped by upstream) — insert directly
    let result = format!(
        "{{\"content\":\"{}\",\"finish_reason\":\"{}\"}}",
        content, finish
    );
    write_json(&result)
}

/// Discover models from Xiaomi OpenAI-compatible GET /v1/models.
/// Host ABI: get_credentials + http_get; return JSON array [{"id","name"}, ...].
#[no_mangle]
pub extern "C" fn list_models() -> u32 {
    unsafe { WRITE_POS = 0; }

    let api_key = get_credential("api_key");
    if api_key.is_empty() {
        // No account yet — fail closed so sync does not wipe DB with empty list.
        return write_json("{\"error\":\"missing api_key\"}");
    }

    let base = get_credential_base_url();
    let base = if base.is_empty() {
        "https://api.xiaomimimo.com/v1".to_string()
    } else {
        base.trim_end_matches('/').to_string()
    };
    let url = format!("{}/models", base);
    let (uptr, ulen) = str_to_ptr(&url);
    let hdrs = format!(
        "{{\"Authorization\":\"Bearer {}\",\"Accept\":\"application/json\"}}",
        json_escape(&api_key)
    );
    let (hptr, hlen) = str_to_ptr(&hdrs);

    let resp_ptr = unsafe { http_get(uptr, ulen, hptr, hlen) };
    if resp_ptr == 0 {
        return write_json("{\"error\":\"upstream failed\"}");
    }
    let resp_bytes = read_host_json(resp_ptr);
    let resp_str = match core::str::from_utf8(resp_bytes) {
        Ok(s) => s,
        Err(_) => return write_json("{\"error\":\"invalid upstream encoding\"}"),
    };

    // Host error envelope: {"error":"...","code":"..."}.
    if resp_str.contains("\"code\":\"") && resp_str.contains("\"error\"") {
        // Pass through short error for host ListModels parse failure → sync aborts.
        return write_json(&format!(
            "{{\"error\":{}}}",
            // keep raw if already json object; else wrap
            if resp_str.starts_with('{') {
                // extract error string if present
                extract_json_string(resp_str, "error")
                    .map(|e| format!("\"{}\"", json_escape(&e)))
                    .unwrap_or_else(|| format!("\"{}\"", json_escape(resp_str)))
            } else {
                format!("\"{}\"", json_escape(resp_str))
            }
        ));
    }

    match parse_openai_model_list(resp_str) {
        Some(json) => write_json(&json),
        None => write_json("{\"error\":\"failed to parse models list\"}"),
    }
}

/// Optional base_url from get_credentials JSON (same blob as api_key).
fn get_credential_base_url() -> String {
    let (kptr, klen) = str_to_ptr("api_key");
    let ptr = unsafe { get_credentials(kptr, klen) };
    if ptr == 0 {
        return String::new();
    }
    // Host writes length-prefixed JSON outside WRITE_BUF.
    let raw = read_host_json(ptr);
    let s = core::str::from_utf8(raw).unwrap_or("");
    extract_json_string(s, "base_url").unwrap_or_default()
}

/// Parse OpenAI-style {"object":"list","data":[{"id":"..."},...]} into
/// [{"id":"...","name":"..."},...] for host DiscoveredModel.
fn parse_openai_model_list(body: &str) -> Option<String> {
    let data_key = "\"data\"";
    let data_pos = body.find(data_key)?;
    let after = &body[data_pos + data_key.len()..];
    let bracket = after.find('[')?;
    let arr = &after[bracket..];

    let mut models: Vec<(String, String)> = Vec::new();
    let mut i = 0;
    let bytes = arr.as_bytes();
    while i + 5 < bytes.len() {
        // scan for "id":"
        if bytes[i] == b'"' && i + 5 < bytes.len() && &arr[i..i + 5] == "\"id\":" {
            let rest = &arr[i + 5..];
            let rest = rest.trim_start();
            if let Some(stripped) = rest.strip_prefix('"') {
                if let Some(end) = stripped.find('"') {
                    let id = &stripped[..end];
                    if !id.is_empty() && !models.iter().any(|(x, _)| x == id) {
                        models.push((id.to_string(), id.to_string()));
                    }
                    i += 5 + (rest.len() - stripped.len()) + end + 1;
                    continue;
                }
            }
        }
        // end of data array
        if bytes[i] == b']' && models.len() > 0 {
            break;
        }
        i += 1;
    }

    if models.is_empty() {
        return None;
    }

    let mut out = String::from("[");
    for (idx, (id, name)) in models.iter().enumerate() {
        if idx > 0 {
            out.push(',');
        }
        out.push_str("{\"id\":\"");
        out.push_str(&json_escape(id));
        out.push_str("\",\"name\":\"");
        out.push_str(&json_escape(name));
        out.push_str("\"}");
    }
    out.push(']');
    Some(out)
}
