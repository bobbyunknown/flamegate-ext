// FlameGate WASM Extension — Google Antigravity & CodeAssist (Rust)
// Provides Gemini & Claude models via Google OAuth and v1internal CodeAssist APIs.

#![allow(static_mut_refs)]

#[link(wasm_import_module = "env")]
extern "C" {
    fn http_post(
        url_ptr: u32, url_len: u32,
        body_ptr: u32, body_len: u32,
        hdrs_ptr: u32, hdrs_len: u32,
    ) -> u32;
    fn http_get(
        url_ptr: u32, url_len: u32,
        hdrs_ptr: u32, hdrs_len: u32,
    ) -> u32;
    fn get_credentials(key_ptr: u32, key_len: u32) -> u32;
    fn emit_chunk(chunk_ptr: u32, chunk_len: u32);
}

static mut WRITE_BUF: [u8; 512 * 1024] = [0u8; 512 * 1024];
static mut WRITE_POS: u32 = 0;

// API URLs
const OAUTH_AUTH_URL: &str = "https://accounts.google.com/o/oauth2/v2/auth";
const OAUTH_TOKEN_URL: &str = "https://oauth2.googleapis.com/token";
const USER_INFO_URL: &str = "https://www.googleapis.com/oauth2/v2/userinfo";
const GENERATE_URL: &str = "https://daily-cloudcode-pa.googleapis.com/v1internal:generateContent";
const STREAM_GENERATE_URL: &str = "https://daily-cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse";

const ANTIGRAVITY_UA: &str = "antigravity/cli/1.1.22 (aidev_client; os_type=darwin; arch=amd64; cl=971564011; auth_method=consumer)";
const OAUTH_SCOPES: &str = "https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile https://www.googleapis.com/auth/cclog https://www.googleapis.com/auth/experimentsandconfigs https://www.googleapis.com/auth/aicode";

fn decode_xor(bytes: &[u8], key: u8) -> String {
    let dec: Vec<u8> = bytes.iter().map(|&b| b ^ key).collect();
    String::from_utf8(dec).unwrap_or_default()
}

fn client_id() -> String {
    let enc: [u8; 73] = [
        107, 106, 109, 107, 106, 106, 108, 106, 108, 106, 111, 99, 107, 119, 46, 55, 50, 41, 41,
        51, 52, 104, 50, 104, 107, 54, 57, 40, 63, 104, 105, 111, 44, 46, 53, 54, 53, 48, 50,
        110, 61, 110, 106, 105, 63, 42, 116, 59, 42, 42, 41, 116, 61, 53, 53, 61, 54, 63, 47,
        41, 63, 40, 57, 53, 52, 46, 63, 52, 46, 116, 57, 53, 55,
    ];
    decode_xor(&enc, 0x5A)
}

fn client_secret() -> String {
    let enc: [u8; 35] = [
        29, 21, 25, 9, 10, 2, 119, 17, 111, 98, 28, 13, 8, 110, 98, 108, 22, 62, 22, 16, 107,
        55, 22, 24, 98, 41, 2, 25, 110, 32, 108, 43, 30, 27, 60,
    ];
    decode_xor(&enc, 0x5A)
}

fn buf_base() -> u32 {
    unsafe { WRITE_BUF.as_ptr() as u32 }
}

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

fn read_host_json(ptr: u32) -> &'static [u8] {
    if ptr == 0 {
        return &[];
    }
    unsafe {
        let len_bytes = core::slice::from_raw_parts(ptr as *const u8, 4);
        let len = u32::from_le_bytes([len_bytes[0], len_bytes[1], len_bytes[2], len_bytes[3]]);
        core::slice::from_raw_parts((ptr + 4) as *const u8, len as usize)
    }
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
    let raw = read_host_json(ptr);
    let s = core::str::from_utf8(raw).unwrap_or("");
    if let Some(v) = extract_json_string(s, key) {
        if !v.is_empty() {
            return v;
        }
    }
    if let Some(v) = extract_json_string(s, "access_token") {
        if !v.is_empty() {
            return v;
        }
    }
    if let Some(v) = extract_json_string(s, "api_key") {
        if !v.is_empty() {
            return v;
        }
    }
    String::new()
}

fn extract_json_string(json: &str, key: &str) -> Option<String> {
    let key_pattern = format!("\"{}\"", key);
    let key_pos = json.find(&key_pattern)?;
    let after_key = &json[key_pos + key_pattern.len()..];
    let colon_pos = after_key.find(':')?;
    let after_colon = after_key[colon_pos + 1..].trim_start();
    if after_colon.starts_with('"') {
        let after_quote = &after_colon[1..];
        let mut end = 0;
        let mut escaped = false;
        for (i, c) in after_quote.char_indices() {
            if escaped {
                escaped = false;
            } else if c == '\\' {
                escaped = true;
            } else if c == '"' {
                end = i;
                break;
            }
        }
        Some(after_quote[..end].to_string())
    } else {
        None
    }
}

fn extract_candidate_text(json: &str) -> String {
    let mut text_acc = String::new();
    let search_key = "\"text\"";
    let mut offset = 0;
    while let Some(pos) = json[offset..].find(search_key) {
        let abs_pos = offset + pos;
        let after_key = &json[abs_pos + search_key.len()..];
        if let Some(colon_pos) = after_key.find(':') {
            let after_colon = after_key[colon_pos + 1..].trim_start();
            if after_colon.starts_with('"') {
                let after_quote = &after_colon[1..];
                let mut end = 0;
                let mut escaped = false;
                for (i, c) in after_quote.char_indices() {
                    if escaped {
                        escaped = false;
                    } else if c == '\\' {
                        escaped = true;
                    } else if c == '"' {
                        end = i;
                        break;
                    }
                }
                let part_text = &after_quote[..end];
                text_acc.push_str(part_text);
                offset = abs_pos + search_key.len() + 1 + colon_pos + 1 + end + 1;
                continue;
            }
        }
        offset = abs_pos + search_key.len();
    }
    text_acc
}

fn extract_json_bool(json: &str, key: &str) -> Option<bool> {
    let key_pattern = format!("\"{}\"", key);
    let key_pos = json.find(&key_pattern)?;
    let after_key = &json[key_pos + key_pattern.len()..];
    let colon_pos = after_key.find(':')?;
    let rest = after_key[colon_pos + 1..].trim_start();
    if rest.starts_with("true") {
        Some(true)
    } else if rest.starts_with("false") {
        Some(false)
    } else {
        None
    }
}

fn url_encode(s: &str) -> String {
    let mut result = String::new();
    for b in s.bytes() {
        match b {
            b'a'..=b'z' | b'A'..=b'Z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' => {
                result.push(b as char);
            }
            b' ' => result.push('+'),
            _ => {
                result.push_str(&format!("%{:02X}", b));
            }
        }
    }
    result
}

fn json_escape(s: &str) -> String {
    let mut out = String::new();
    for c in s.chars() {
        match c {
            '"' => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            '\n' => out.push_str("\\n"),
            '\r' => out.push_str("\\r"),
            '\t' => out.push_str("\\t"),
            _ => out.push(c),
        }
    }
    out
}

// Capability: oauth_authorize
fn handle_oauth_authorize(payload: &str) -> u32 {
    let redirect_uri = extract_json_string(payload, "redirect_uri").unwrap_or_default();
    let state = extract_json_string(payload, "state").unwrap_or_default();

    let auth_url = format!(
        "{}?access_type=offline&prompt=consent&response_type=code&client_id={}&redirect_uri={}&scope={}&state={}&include_granted_scopes=true",
        OAUTH_AUTH_URL,
        url_encode(&client_id()),
        url_encode(&redirect_uri),
        url_encode(OAUTH_SCOPES),
        url_encode(&state)
    );

    let resp = format!("{{\"url\":\"{}\",\"state\":\"{}\"}}", json_escape(&auth_url), json_escape(&state));
    write_json(&resp)
}

// Capability: oauth_exchange
fn handle_oauth_exchange(payload: &str) -> u32 {
    let code = extract_json_string(payload, "code").unwrap_or_default();
    let redirect_uri = extract_json_string(payload, "redirect_uri").unwrap_or_default();

    let form_body = format!(
        "grant_type=authorization_code&client_id={}&client_secret={}&code={}&redirect_uri={}",
        url_encode(&client_id()),
        url_encode(&client_secret()),
        url_encode(&code),
        url_encode(&redirect_uri)
    );

    let hdrs = "{\"Content-Type\":\"application/x-www-form-urlencoded\",\"Accept\":\"application/json\"}";
    let (u_ptr, u_len) = str_to_ptr(OAUTH_TOKEN_URL);
    let (b_ptr, b_len) = str_to_ptr(&form_body);
    let (h_ptr, h_len) = str_to_ptr(hdrs);

    let token_resp_ptr = unsafe { http_post(u_ptr, u_len, b_ptr, b_len, h_ptr, h_len) };
    if token_resp_ptr == 0 {
        return write_json("{\"error\":\"failed to exchange token from Google OAuth: http_post returned null\"}");
    }

    let token_raw = read_host_json(token_resp_ptr);
    let token_json = core::str::from_utf8(token_raw).unwrap_or("");
    let access_token = extract_json_string(token_json, "access_token").unwrap_or_default();
    let refresh_token = extract_json_string(token_json, "refresh_token").unwrap_or_default();

    if access_token.is_empty() {
        return write_json(&format!("{{\"error\":\"no access token in response: {}\"}}", json_escape(token_json)));
    }

    // Fetch user info for account identification
    let mut account_name = String::from("antigravity-user");
    let user_hdrs = format!("{{\"Authorization\":\"Bearer {}\",\"Accept\":\"application/json\"}}", json_escape(&access_token));
    let (uu_ptr, uu_len) = str_to_ptr(USER_INFO_URL);
    let (uh_ptr, uh_len) = str_to_ptr(&user_hdrs);
    let user_resp_ptr = unsafe { http_get(uu_ptr, uu_len, uh_ptr, uh_len) };
    if user_resp_ptr != 0 {
        let user_raw = read_host_json(user_resp_ptr);
        let user_json = core::str::from_utf8(user_raw).unwrap_or("");
        if let Some(email) = extract_json_string(user_json, "email") {
            if !email.is_empty() {
                account_name = email;
            }
        }
    }

    let result = format!(
        "{{\"access_token\":\"{}\",\"refresh_token\":\"{}\",\"expires_in\":3599,\"email\":\"{}\",\"account_name\":\"{}\",\"project_id\":\"aicode-consumers\"}}",
        json_escape(&access_token),
        json_escape(&refresh_token),
        json_escape(&account_name),
        json_escape(&account_name)
    );
    write_json(&result)
}

// Capability: oauth_refresh
fn handle_oauth_refresh(payload: &str) -> u32 {
    let refresh_token = extract_json_string(payload, "refresh_token").unwrap_or_default();
    if refresh_token.is_empty() {
        return write_json("{\"error\":\"missing refresh_token\"}");
    }

    let form_body = format!(
        "client_id={}&client_secret={}&refresh_token={}&grant_type=refresh_token",
        url_encode(&client_id()),
        url_encode(&client_secret()),
        url_encode(&refresh_token)
    );

    let hdrs = "{\"Content-Type\":\"application/x-www-form-urlencoded\",\"Accept\":\"application/json\"}";
    let (u_ptr, u_len) = str_to_ptr(OAUTH_TOKEN_URL);
    let (b_ptr, b_len) = str_to_ptr(&form_body);
    let (h_ptr, h_len) = str_to_ptr(hdrs);

    let token_resp_ptr = unsafe { http_post(u_ptr, u_len, b_ptr, b_len, h_ptr, h_len) };
    if token_resp_ptr == 0 {
        return write_json("{\"error\":\"failed to refresh token from Google OAuth: http_post returned null\"}");
    }

    let token_raw = read_host_json(token_resp_ptr);
    let token_json = core::str::from_utf8(token_raw).unwrap_or("");
    let access_token = extract_json_string(token_json, "access_token").unwrap_or_default();
    let new_refresh_token = extract_json_string(token_json, "refresh_token").unwrap_or(refresh_token);

    let result = format!(
        "{{\"access_token\":\"{}\",\"refresh_token\":\"{}\",\"expires_in\":3599}}",
        json_escape(&access_token),
        json_escape(&new_refresh_token)
    );
    write_json(&result)
}

#[no_mangle]
pub extern "C" fn list_models() -> u32 {
    reset_mem();
    let models_json = r#"[
        {"id":"gemini-3.7-flash-high","name":"Gemini 3.7 Flash (High)","tier":"flash","tags":["flash","agent"]},
        {"id":"gemini-3.7-flash-medium","name":"Gemini 3.7 Flash (Medium)","tier":"flash","tags":["flash"]},
        {"id":"gemini-3.7-flash-low","name":"Gemini 3.7 Flash (Low)","tier":"flash","tags":["flash"]},
        {"id":"gemini-3.6-flash-high","name":"Gemini 3.6 Flash (High)","tier":"flash","tags":["flash"]},
        {"id":"gemini-3.6-flash-medium","name":"Gemini 3.6 Flash (Medium)","tier":"flash","tags":["flash"]},
        {"id":"gemini-3.6-flash-low","name":"Gemini 3.6 Flash (Low)","tier":"flash","tags":["flash"]},
        {"id":"gemini-3-flash-agent","name":"Gemini 3.5 Flash (High)","tier":"flash","tags":["flash","agent"]},
        {"id":"gemini-3.5-flash-low","name":"Gemini 3.5 Flash (Medium)","tier":"flash","tags":["flash"]},
        {"id":"gemini-3.5-flash-extra-low","name":"Gemini 3.5 Flash (Low)","tier":"flash","tags":["flash"]},
        {"id":"gemini-pro-agent","name":"Gemini 3.1 Pro (High)","tier":"pro","tags":["pro","agent"]},
        {"id":"gemini-3.1-pro-low","name":"Gemini 3.1 Pro (Low)","tier":"pro","tags":["pro"]},
        {"id":"claude-sonnet-4-6","name":"Claude Sonnet 4.6 (Thinking)","tier":"frontier","tags":["frontier","thinking"]},
        {"id":"claude-opus-4-6-thinking","name":"Claude Opus 4.6 (Thinking)","tier":"frontier","tags":["frontier","thinking"]},
        {"id":"gpt-oss-120b-medium","name":"GPT-OSS 120B (Medium)","tier":"free","tags":["free","open-source"]},
        {"id":"gemini-3-flash","name":"Gemini 3 Flash","tier":"flash","tags":["flash"]},
        {"id":"gemini-3.1-flash-image","name":"Gemini 3.1 Flash (Image)","tier":"image","tags":["image"]},
        {"id":"gemini-2.5-flash","name":"Gemini 2.5 Flash","tier":"flash","tags":["flash"]},
        {"id":"gemini-2.5-pro","name":"Gemini 2.5 Pro","tier":"pro","tags":["pro"]}
    ]"#;
    write_json(models_json)
}

#[no_mangle]
pub extern "C" fn invoke(ptr: u32, _len: u32) -> u32 {
    let req_raw = read_host_json(ptr);
    let req_json = core::str::from_utf8(req_raw).unwrap_or("");

    // Capability dispatch for OAuth
    if let Some(cap) = extract_json_string(req_json, "capability") {
        match cap.as_str() {
            "oauth_authorize" => return handle_oauth_authorize(req_json),
            "oauth_exchange" => return handle_oauth_exchange(req_json),
            "oauth_refresh" => return handle_oauth_refresh(req_json),
            _ => return write_json(&format!("{{\"error\":\"unsupported capability: {}\"}}", cap)),
        }
    }

    let access_token = get_credential("access_token");
    let model = extract_json_string(req_json, "model").unwrap_or_else(|| "gemini-2.5-flash".to_string());
    let stream = extract_json_bool(req_json, "stream").unwrap_or(false);

    // Extract prompt / messages
    let mut user_text = String::new();
    let mut system_text = String::new();

    let mut search_idx = 0;
    while let Some(msg_pos) = req_json[search_idx..].find("{\"role\":") {
        let abs_pos = search_idx + msg_pos;
        let sub = &req_json[abs_pos..];
        if let Some(role) = extract_json_string(sub, "role") {
            if let Some(content) = extract_json_string(sub, "content") {
                if role == "system" {
                    if !system_text.is_empty() {
                        system_text.push('\n');
                    }
                    system_text.push_str(&content);
                } else if role == "user" || role == "assistant" {
                    if !user_text.is_empty() {
                        user_text.push('\n');
                    }
                    user_text.push_str(&content);
                }
            } else if let Some(text_val) = extract_json_string(sub, "text") {
                if role == "system" {
                    if !system_text.is_empty() {
                        system_text.push('\n');
                    }
                    system_text.push_str(&text_val);
                } else if role == "user" || role == "assistant" {
                    if !user_text.is_empty() {
                        user_text.push('\n');
                    }
                    user_text.push_str(&text_val);
                }
            }
        }
        search_idx = abs_pos + 8;
    }

    if user_text.is_empty() {
        if let Some(prompt) = extract_json_string(req_json, "text") {
            user_text = prompt;
        } else {
            user_text = "Hello".to_string();
        }
    }

    // Build internal v1internal envelope
    let sys_instruction = if !system_text.is_empty() {
        format!(",\"systemInstruction\":{{\"parts\":[{{\"text\":\"{}\"}}]}}", json_escape(&system_text))
    } else {
        String::new()
    };

    let inner_req = format!(
        "{{\"contents\":[{{\"role\":\"user\",\"parts\":[{{\"text\":\"{}\"}}]}}]{},\"generationConfig\":{{\"temperature\":0.7}}}}",
        json_escape(&user_text),
        sys_instruction
    );

    let envelope = format!(
        "{{\"project\":\"aicode-consumers\",\"model\":\"{}\",\"userAgent\":\"antigravity\",\"requestType\":\"agent\",\"requestId\":\"agent/00000000-0000-0000-0000-000000000000/1724948000000/00000000-0000-0000-0000-000000000000/1\",\"request\":{}}}",
        json_escape(&model),
        inner_req
    );

    let hdrs = format!(
        "{{\"Authorization\":\"Bearer {}\",\"Content-Type\":\"application/json\",\"User-Agent\":\"{}\"}}",
        json_escape(&access_token),
        json_escape(ANTIGRAVITY_UA)
    );

    if stream {
        let (u_ptr, u_len) = str_to_ptr(STREAM_GENERATE_URL);
        let (b_ptr, b_len) = str_to_ptr(&envelope);
        let (h_ptr, h_len) = str_to_ptr(&hdrs);

        let resp_ptr = unsafe { http_post(u_ptr, u_len, b_ptr, b_len, h_ptr, h_len) };
        if resp_ptr != 0 {
            let resp_raw = read_host_json(resp_ptr);
            let resp_str = core::str::from_utf8(resp_raw).unwrap_or("");

            for line in resp_str.lines() {
                let trimmed = line.trim();
                if let Some(data_str) = trimmed.strip_prefix("data:") {
                    let payload = data_str.trim();
                    if payload == "[DONE]" || payload.is_empty() {
                        continue;
                    }
                    let text = extract_candidate_text(payload);
                    if !text.is_empty() {
                        let chunk = format!(
                            "{{\"choices\":[{{\"delta\":{{\"content\":\"{}\"}},\"index\":0}}]}}",
                            json_escape(&text)
                        );
                        emit(chunk.as_bytes());
                    }
                }
            }
        }
        let finish_chunk = "{\"choices\":[{\"delta\":{\"content\":\"\"},\"index\":0,\"finish_reason\":\"stop\"}]}";
        emit(finish_chunk.as_bytes());
        return 0;
    } else {
        let (u_ptr, u_len) = str_to_ptr(GENERATE_URL);
        let (b_ptr, b_len) = str_to_ptr(&envelope);
        let (h_ptr, h_len) = str_to_ptr(&hdrs);

        let resp_ptr = unsafe { http_post(u_ptr, u_len, b_ptr, b_len, h_ptr, h_len) };
        let mut reply_text = String::new();
        if resp_ptr != 0 {
            let resp_raw = read_host_json(resp_ptr);
            let resp_str = core::str::from_utf8(resp_raw).unwrap_or("");
            if resp_str.contains("\"error\"") {
                return write_json(resp_str);
            }
            reply_text = extract_candidate_text(resp_str);
        }

        let resp = format!(
            "{{\"content\":\"{}\",\"finish_reason\":\"stop\"}}",
            json_escape(&reply_text)
        );
        return write_json(&resp);
    }
}
