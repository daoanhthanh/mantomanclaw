# Kiến Trúc Multi-Tenant và Triết Lý Bảo Mật

ManToManAgent là **gateway AI agent** — xử lý agent, chat, session, tool, MCP server, và memory. Hỗ trợ hai chế độ triển khai:

1. **Cá nhân / Single-tenant** — Dùng ManToManAgent trực tiếp làm backend AI của bạn. Dashboard tích hợp sẵn.
2. **SaaS / Multi-tenant** — Tích hợp ManToManAgent phía sau ứng dụng của bạn. API key làm cầu nối giữa hai hệ thống.

---

## Triết Lý Bảo Mật: Defense-in-Depth (Phòng Thủ Nhiều Tầng)

ManToManAgent áp dụng **defense-in-depth** với **năm lớp bảo vệ độc lập** từ transport đến isolation. Mỗi lớp hoạt động độc lập — ngay cả khi một lớp bị vượt qua, các lớp còn lại vẫn tiếp tục bảo vệ hệ thống.

```mermaid
flowchart TD
    REQ["Request"] --> L1["Tầng 1: Transport<br/>CORS, giới hạn kích thước, timing-safe auth"]
    L1 --> L2["Tầng 2: Input<br/>Phát hiện injection (6 pattern), cắt bớt message"]
    L2 --> L3["Tầng 3: Tool<br/>Shell deny pattern, path traversal, SSRF, exec approval"]
    L3 --> L4["Tầng 4: Output<br/>Scrub credential, content wrapping"]
    L4 --> L5["Tầng 5: Isolation<br/>Workspace isolation, Docker sandbox, read-only FS"]
```

### Tầng 1: Bảo Mật Transport

| Cơ Chế | Chi Tiết |
|--------|----------|
| CORS (WebSocket) | `checkOrigin()` xác thực với `allowed_origins` (rỗng = cho phép tất cả, để tương thích ngược) |
| Giới hạn WS message | `SetReadLimit(512KB)` — gorilla tự động đóng kết nối khi vượt quá |
| Giới hạn HTTP body | `MaxBytesReader(1MB)` — trả về lỗi trước khi decode JSON |
| Token auth | `crypto/subtle.ConstantTimeCompare` (timing-safe, chống timing attack) |
| Rate limiting | Token bucket per user/IP, cấu hình qua `rate_limit_rpm` |

### Tầng 2: Input — Phát Hiện Injection

Input guard quét 6 pattern injection:

| Pattern | Target | Regex Match |
|---------|--------|-------------|
| `ignore_instructions` | "ignore all previous instructions" | Không phân biệt hoa thường: ignore + (all?)previous/prior/above/earlier/preceding + instructions/rules/prompts/directives/guidelines |
| `role_override` | "you are now...", "pretend you are..." | Không phân biệt hoa thường: (you are now\|from now on you are\|pretend you are\|act as if you are\|imagine you are) |
| `system_tags` | `<system>`, `[SYSTEM]`, `[INST]`, `<<SYS>>` | Không phân biệt hoa thường: `</?system>`, `[SYSTEM]`, `[INST]`, `<<SYS>>`, `<\|im_start\|>system` |
| `instruction_injection` | "new instructions:", "override:", "system prompt:" | Không phân biệt hoa thường: (new instructions?\|override\|system prompt\|<\|system\|>) |
| `null_bytes` | Ký tự null `\x00` (nỗ lực obfuscation) | Phát hiện byte `\x00` thô |
| `delimiter_escape` | "end of system", "begin user input", `</instructions>`, `</prompt>` | Không phân biệt hoa thường: (end of system\|begin user input\|`</?instructions?>`\|`</rules>`\|`</prompt>`\|`</context>`) |

**Hành động cấu hình được** (`gateway.injection_action`):

| Giá Trị | Hành Vi |
|---------|---------|
| `"log"` | Ghi log info level, tiếp tục xử lý |
| `"warn"` (mặc định) | Ghi log warning level, tiếp tục xử lý |
| `"block"` | Ghi log warning, trả về lỗi, dừng xử lý |
| `"off"` | Tắt hoàn toàn detection |

**Cắt bớt message**: Message vượt quá `max_message_chars` (mặc định 32K) được cắt bớt (không reject), và LLM được thông báo về việc cắt bớt.

### Tầng 3: Bảo Mật Tool

**Shell deny pattern** — 7 loại lệnh bị chặn (xem `internal/tools/shell.go`):

| Loại | Ví Dụ |
|------|-------|
| Thao tác file hủy diệt | `rm -rf`, `del /f`, `rmdir /s` |
| Thao tác disk hủy diệt | `mkfs`, `dd if=`, `> /dev/sd*` |
| Lệnh hệ thống | `shutdown`, `reboot`, `poweroff` |
| Fork bomb | `:(){ ... };:` |
| Remote code execution | `curl \| sh`, `wget -O - \| sh` |
| Reverse shell | `/dev/tcp/`, `nc -e` |
| Eval injection | `eval $()`, `base64 -d \| sh` |

Lệnh được quét tại thời điểm thực thi qua regex deny list. Pattern có thể cấu hình per-binary qua `exec_settings.deny_patterns` (mặc định đã hardened cho operation hủy diệt/exfil). Chặn verbose flag (deny_verbose list) ngăn leakage output nhạy cảm.

**Bảo vệ SSRF** — 3 bước xác thực:

```mermaid
flowchart TD
    URL["URL cần fetch"] --> S1["Bước 1: Kiểm tra hostname bị chặn<br/>localhost, *.local, *.internal,<br/>metadata.google.internal"]
    S1 --> S2["Bước 2: Kiểm tra dải IP private<br/>10.0.0.0/8, 172.16.0.0/12,<br/>192.168.0.0/16, 127.0.0.0/8,<br/>169.254.0.0/16, IPv6 loopback/link-local"]
    S2 --> S3["Bước 3: DNS Pinning<br/>Resolve domain, kiểm tra mọi IP resolved.<br/>Cũng áp dụng cho redirect target."]
    S3 --> ALLOW["Cho phép request"]
```

**MCP stdio validation** — Config MCP server do admin tạo, import, test, và khám phá on-demand đều phải qua `internal/mcp.ValidateServerConfig()` trước khi process client tạm thời hoặc persistent được tạo. Config `stdio` bị giới hạn ở runtime name trong allowlist được resolve từ `PATH`; lệnh có path như `./node`, `tools/node`, `/tmp/node`, hoặc `.\\node.exe` bị reject để ngăn wrapper substitution. Argument reject shell metacharacter và eval/import flag, và chặn remote loader/script/package execution mode như `node --loader`, `python -m`, `deno`/`bun` remote ref, `npx`/`uvx`/`pipx` package target, `uv --with`, `npm exec`, `go run`, `cargo install`, và `dotnet tool install`.

**Path traversal**: `resolvePath()` áp dụng `filepath.Clean()` rồi `HasPrefix()` để đảm bảo mọi path nằm trong workspace. Với `restrict = true`, mọi path ngoài workspace bị chặn.

**PathDenyable** — Interface cho phép filesystem tool reject path prefix cụ thể:

```go
type PathDenyable interface {
    DenyPaths(...string)
}
```

Cả bốn filesystem tool (`read_file`, `write_file`, `list_files`, `edit`) implement `PathDenyable`. Agent loop gọi `DenyPaths(".mantomanagent")` lúc startup để ngăn agent truy cập internal data directory. `list_files` còn filter denied directory hoàn toàn khỏi output — agent không thấy denied path trong directory listing.

#### Credentialed Exec Security

**Direct Exec Mode** cho credentialed CLI tool implement defense-in-depth với 4 lớp độc lập:

| Tầng | Cơ Chế | Bảo Vệ Chống |
|------|--------|--------------|
| **No shell** | `exec.CommandContext(binary, args...)` (không bao giờ `sh -c`) | Shell command injection, credential leakage qua env var expansion |
| **Path verify** | `exec.LookPath()` + config match check | Binary spoofing (ví dụ `./gh` trong workspace) |
| **Deny pattern** | Per-binary regex deny list trên argument + verbose flag | Sensitive operation per CLI (ví dụ `auth`, `ssh-key`) |
| **Output scrub** | Credential value được đăng ký để scrub động | Credential trong stdout/stderr |

| Attack Surface | Mitigation |
|----------------|------------|
| Shell operator injection, argument injection qua space | Early regex scan + shell-word parsing (không shell eval) |
| Binary PATH manipulation, symlink attack | `exec.LookPath()` + yêu cầu absolute path + config match |
| Env var exfiltration, output parsing trick | No-shell exec (env var không bao giờ expand) + dynamic credential scrubbing |
| Timeout abuse | Cấu hình timeout per-binary với context deadline |
| Sandbox escape | Docker container isolation khi sandbox enabled |
| Verbose flag leakage | Deny_verbose list riêng chặn verbose/debug output |

**Agent-level grant enforcement** — Gate chạy **trước** mọi process spawn, chặn agent chưa được grant khỏi thực thi registered binary:

| Kiểm Soát | Triển Khai |
|-----------|------------|
| **Grant lookup** | `store.SecureCLIStore.IsRegisteredBinary(ctx, binaryName)` kiểm tra bảng `secure_cli_agent_grants`. Binary không global yêu cầu row cho agent đang gọi. |
| **Fail-CLOSED** | Nếu grant lookup lỗi (DB down, timeout), exec bị deny với retry message. Per-lookup timeout: 2 giây. |
| **Env scrubbing** | Khi lệnh thoát khỏi credentialed path (ví dụ qua adversarial `exec` tool), child process env bị scrub mọi credential key (static deny list + dynamic key từ mọi registered binary trong tenant) trước khi spawn. Ngăn credential leakage vào non-credentialed command. |
| **Wrapper unwrap** | Chặn shell wrapper (`sh -c`, `bash -c`, etc.) cố evade binary path matching. Kiểm tra tới 3 cấp lồng nhau; chuỗi sâu hơn bị reject là adversarial. |
| **Logging** | Ba security event: `security.credentialed_binary_denied` (agent chưa grant), `security.credentialed_binary_gate_error` (lookup failure), `security.credentialed_binary_wrapper_too_deep` (nested wrapper attack). Tất cả có: binary, wrapper, agent_id, tenant_id, command prefix. |
| **Subagent wiring** | Subagent `ExecTool` dùng cùng `SecureCLIStore` qua `cmd/gateway_agents.go` → `buildSubagentToolsRegistry`. Parent agent không thể bypass gate bằng cách delegate exec cho spawned subagent. |

### Tầng 4: Bảo Mật Output

| Cơ Chế | Chi Tiết |
|--------|----------|
| Static credential scrubbing | Regex pattern phát hiện: OpenAI (`sk-[a-zA-Z0-9]{20,}`), Anthropic (`sk-ant-[a-zA-Z0-9-]{20,}`), GitHub token (`ghp_/gho_/ghu_/ghs_/ghr_` + 36 ký tự), AWS (`AKIA[A-Z0-9]{16}`), generic pattern (API/token/secret/password/bearer/authorization + 8+ ký tự), connection string (postgres/mysql/mongodb/redis/amqp URL), env var (KEY/SECRET/CREDENTIAL/PRIVATE + 8+ ký tự, DSN/DATABASE_URL/REDIS_URL/MONGO_URI), VIRTUAL_* var (4+ ký tự), long hex string (64+ ký tự). Tất cả thay bằng `[REDACTED]`. |
| Dynamic credential scrubbing | Credential value đăng ký runtime (tối thiểu 6 ký tự) được scrub qua `AddCredentialScrubValues()` và thay bằng `[REDACTED]` |
| Dynamic value scrubbing (SSRF) | Server IP và value khám phá runtime khác được đăng ký qua `AddDynamicScrubValues()` và thay bằng `[SERVER_IP]` |
| Web content wrapping | Content fetch được bọc trong tag `<<<EXTERNAL_UNTRUSTED_CONTENT>>>` với cảnh báo security |

### Passive Channel Memory Redaction

Passive channel memory extraction chạy redaction pass bổ sung trước LLM trên pending group message trước khi candidate bền vững được tạo. Pass này xóa secret/key assignment phổ biến, bearer/provider token, connection string, chuỗi số giống payment, số điện thoại, địa chỉ email, configured excluded sender ID, và configured regex exclusion pattern. Bảng extraction mới lưu redaction count/type và extracted summary chứ không phải raw message body.

### Tầng 5: Isolation

**Per-user workspace isolation** — Hai cấp ngăn truy cập file cross-user:

| Cấp | Scope | Directory Pattern |
|-----|-------|-------------------|
| Per-agent | Mỗi agent có base directory riêng | `~/.mantomanagent/{agent-key}-workspace/` |
| Per-user | Mỗi user có subdirectory trong agent workspace | `{agent-workspace}/user_{sanitized_id}/` |

Workspace được inject vào tool qua `WithToolWorkspace(ctx)` context injection. Tool đọc workspace từ context lúc thực thi (fallback về struct field để tương thích ngược). User ID được sanitized: bất kỳ thứ gì ngoài `[a-zA-Z0-9_-]` thành underscore (`group:telegram:-1001234` → `group_telegram_-1001234`).

**Privilege separation cho package management** — System package (apk) được cài qua root-privileged helper:

| Component | User | Scope | Socket |
|-----------|------|-------|--------|
| Main app | mantomanagent (1000) | Mọi operation trừ system package | N/A |
| pkg-helper | root | System package (apk) install/uninstall only | `/tmp/pkg.sock` (0660 root:mantomanagent) |

pkg-helper được start trong `docker-entrypoint.sh` *trước khi* privilege bị drop xuống mantomanagent. Main app kết nối tới Unix socket để request apk operation. System package được persist vào `/app/data/.runtime/apk-packages` nên survive container recreation. Python và npm package được cài trực tiếp bởi mantomanagent user vào writable runtime directory (`$PIP_TARGET`, `$NPM_CONFIG_PREFIX`).

**Docker sandbox** — Container-based isolation cho shell command execution:

| Hardening | Configuration |
|-----------|---------------|
| Read-only root FS | `--read-only` |
| Drop all capability | `--cap-drop ALL` |
| No new privilege | `--security-opt no-new-privileges` |
| Memory limit | 512 MB |
| CPU limit | 1.0 |
| PID limit | Enabled |
| Network disabled | `--network none` |
| Tmpfs mount | `/tmp`, `/var/tmp`, `/run` |
| Output limit | 1 MB |
| Timeout | 300 giây |

---

## Kiến Trúc Multi-Tenant

### Chế Độ 1: Sử Dụng Cá Nhân (Single-Tenant)

Dùng ManToManAgent như standalone AI backend với built-in web dashboard. Không cần frontend hoặc backend riêng.

```mermaid
graph LR
    U[Bạn] -->|browser| GC[ManToManAgent Dashboard<br/>+ Gateway]
    GC --> AG[Agent / Chat / Tool]
    AG --> DB[(PostgreSQL)]
    AG -->|LLM call| LLM[Anthropic / OpenAI / Gemini / ...]
```

**Cách hoạt động:**
- Đăng nhập bằng gateway token qua built-in web dashboard
- Tạo agent, cấu hình LLM provider, chat — tất cả từ dashboard
- Kết nối chat channel (Telegram, Discord, etc.) cho messaging
- Tất cả data nằm dưới default "master" tenant — không cần cấu hình tenant

**Setup:**

```bash
# 1. Build và onboard
go build -o mantomanagent . && ./mantomanagent onboard

# 2. Khởi động gateway
source .env.local && ./mantomanagent

# 3. Mở dashboard tại http://localhost:3777
#    Đăng nhập bằng gateway token + user ID "system"
```

**Khi nào dùng:** AI assistant cá nhân, team nhỏ, self-hosted AI tool, development/testing.

**Scale lên:** Khi bạn cần nhiều môi trường cô lập (client, phòng ban, project), tạo thêm tenant. Multi-tenant feature tự động kích hoạt — không cần migration.

---

### Chế Độ 2: Tích Hợp SaaS (Multi-Tenant)

Tích hợp ManToManAgent làm AI engine phía sau ứng dụng SaaS của bạn. App của bạn xử lý auth, billing, và UI. ManToManAgent xử lý AI. Mỗi tenant hoàn toàn cô lập — agent, session, memory, team, provider, và file.

```mermaid
graph TB
    subgraph "Tenant A"
        FEa[Frontend A]
        BEa[Backend A]
        TGa[Telegram Bot A]
    end

    subgraph "Tenant B"
        FEb[Frontend B]
        BEb[Backend B]
        DCb[Discord Bot B]
    end

    subgraph "ManToManAgent Gateway"
        subgraph "Entry Point"
            HTTP[HTTP API]
            WS[WebSocket]
            CH[Channel Manager]
        end

        TI{Tenant Isolation<br/>Layer}

        subgraph "Tenant-Scoped Engine"
            AG[Agent Loop]
            TOOLS[Tool / Skill / MCP]
            MEM[Memory / KG]
        end

        DB[(PostgreSQL<br/>WHERE tenant_id = $N)]
    end

    FEa -->|authenticated| BEa
    BEa -->|API Key A + user_id| HTTP
    TGa -->|webhook| CH

    FEb -->|authenticated| BEb
    BEb -->|API Key B + user_id| HTTP
    DCb -->|webhook| CH

    HTTP --> TI
    WS --> TI
    CH --> TI

    TI -->|ctx với tenant_id| AG
    AG --> TOOLS
    AG --> MEM
    AG --> DB
    TOOLS --> DB
    MEM --> DB
```

**Cách hoạt động:**
- **Backend của mỗi tenant kết nối qua tenant-bound API key** — ManToManAgent tự động scope mọi data
- **Chat channel** (Telegram, Discord, etc.) kết nối trực tiếp — tenant được resolve từ channel instance config
- **Tenant Isolation Layer** resolve tenant_id từ credential và inject vào Go context
- Mọi SQL query enforce `WHERE tenant_id = $N` — fail-closed, không có cross-tenant leakage

**Khi nào dùng:** Sản phẩm SaaS với AI feature, nền tảng multi-client, white-label AI solution.

### Tóm Tắt Loại Kết Nối

Mọi kết nối đều đi qua **Tenant Isolation Layer** trước khi đến agent engine:

| Kết Nối | Phương Thức Auth | Tenant Resolution | Isolation |
|---------|------------------|-------------------|-----------|
| **HTTP API** | `Bearer` token (API key hoặc gateway token) | Tự động từ `tenant_id` của API key | Per-request |
| **WebSocket** | Token trên `connect` (API key hoặc gateway token) | Tự động từ `tenant_id` của API key | Per-session |
| **Chat Channel** | Không (direct webhook/WS) | Nướng vào channel instance DB config | Per-instance |
| **Dashboard** | Gateway token hoặc browser pairing | Tenant membership của user | Per-session |

**Tenant Isolation Layer** — resolve credential → inject `tenant_id` vào Go `context.Context` → mọi downstream SQL query enforce `WHERE tenant_id = $N`. Fail-closed: thiếu tenant = error, không bao giờ unfiltered data.

---

## Tenant Setup (Chỉ Multi-Tenant)

```mermaid
sequenceDiagram
    participant Admin as System Admin
    participant GC as ManToManAgent API

    Admin->>GC: tenants.create {name: "Acme Corp", slug: "acme"}
    GC-->>Admin: {id: "tenant-uuid", slug: "acme"}

    Admin->>GC: tenants.users.add {tenant_id, user_id: "user-123", role: "admin"}

    Admin->>GC: api_keys.create {tenant_id, scopes: ["operator.read", "operator.write"]}
    GC-->>Admin: {key: "mantomanagent_sk_abc123..."}

    Note over Admin: Lưu API key vào config/secret backend của bạn
```

Mỗi tenant được cô lập: **agent, session, team, memory, LLM provider, MCP server, skill**. Tenant-bound API key tự động scope mọi request — không cần extra header.

---

## Tenant Resolution

ManToManAgent xác định tenant từ credential dùng để kết nối:

| Credential | Tenant Resolution | Use Case |
|------------|-------------------|----------|
| **Gateway token** + owner user ID | Tất cả tenant (cross-tenant) | System administration |
| **Gateway token** + non-owner user ID | Master tenant mặc định, hoặc membership-validated `X-ManToManAgent-Tenant-Id` / `tenant_id` hint | Dashboard user |
| **API key** (tenant-bound) | Tự động từ `tenant_id` của key | Normal SaaS integration |
| **API key** (system-level) + `X-ManToManAgent-Tenant-Id` | Header value (UUID hoặc slug), giữ nguyên role gốc của key | Cross-tenant tool |
| **Browser pairing** | Master tenant mặc định, hoặc membership-validated tenant hint | Dashboard operator |
| **Không có credential** | Master tenant | Loopback local development hoặc explicit `MANTOMANAGENT_ALLOW_INSECURE_NO_AUTH=1` only |

**Owner ID:** Cấu hình qua `MANTOMANAGENT_OWNER_IDS` env var (phân tách bằng dấu phẩy). Chỉ owner mới có cross-tenant access với gateway token. Mặc định: `system`.

**Khuyến nghị cho SaaS**: Dùng **tenant-bound API key**. Tenant được resolve tự động từ key — backend của bạn không cần gửi tenant header nào.

---

## HTTP API

Mọi HTTP endpoint chấp nhận header chuẩn:

| Header | Bắt Buộc | Mô Tả |
|--------|:--------:|-------|
| `Authorization` | Có | `Bearer <api-key-hoặc-gateway-token>` |
| `X-ManToManAgent-User-Id` | Có | User ID của app bạn (tối đa 255 ký tự). Scope session và per-user data |
| `X-ManToManAgent-Tenant-Id` | Không | Tenant UUID hoặc slug. Chỉ cần cho system-level key |
| `X-ManToManAgent-Agent-Id` | Không | Target agent ID (thay thế cho field `model`) |
| `Accept-Language` | Không | Locale cho error message: `en`, `vi`, `zh` |

### Chat (OpenAI-Compatible)

```bash
curl -X POST https://mantomanagent.example.com/v1/chat/completions \
  -H "Authorization: Bearer mantomanagent_sk_abc123..." \
  -H "X-ManToManAgent-User-Id: user-456" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "agent:my-agent",
    "messages": [{"role": "user", "content": "Xin chào"}]
  }'
```

Field `model` dùng format `agent:<agent-key>`. API key bound với tenant "Acme Corp" — response chỉ bao gồm data từ tenant đó.

### List Resource

```bash
# List agent
curl https://mantomanagent.example.com/v1/agents \
  -H "Authorization: Bearer mantomanagent_sk_abc123..." \
  -H "X-ManToManAgent-User-Id: user-456"

# List session
curl https://mantomanagent.example.com/v1/sessions \
  -H "Authorization: Bearer mantomanagent_sk_abc123..." \
  -H "X-ManToManAgent-User-Id: user-456"
```

### System Admin (Cross-Tenant)

```bash
# List agent cho tenant cụ thể (yêu cầu gateway token + owner user ID)
curl https://mantomanagent.example.com/v1/agents \
  -H "Authorization: Bearer $GATEWAY_TOKEN" \
  -H "X-ManToManAgent-Tenant-Id: acme" \
  -H "X-ManToManAgent-User-Id: system"
```

---

## WebSocket Integration

Cho real-time feature (streaming chat, live event), kết nối qua WebSocket:

```mermaid
sequenceDiagram
    participant FE as Frontend của bạn
    participant BE as Backend của bạn
    participant GC as ManToManAgent Gateway

    FE->>BE: User gửi message (authenticated)
    BE->>GC: WS connect {token: "mantomanagent_sk_abc...", user_id: "user-456"}
    GC-->>BE: {role: "operator", tenant_id, tenant_name, tenant_slug}

    BE->>GC: chat.send {agent_key: "my-agent", message: "Xin chào"}
    GC-->>BE: event: agent {type: "chunk", content: "Chào bạn..."}
    BE-->>FE: Stream response tới user
    GC-->>BE: event: agent {type: "run.completed"}
```

Sau `connect`, **mọi method tự động được scope** tới tenant của API key. Event được filter server-side — backend của bạn chỉ nhận event thuộc tenant của nó.

**Protocol**: Frame type `req` (client→server), `res` (server→client), `event` (async push). Protocol version 3.

---

## Chat Channel

Chat channel (Telegram, Discord, Zalo, Slack, WhatsApp, Feishu) kết nối **trực tiếp** tới ManToManAgent — không cần API key. Tenant isolation được nướng vào channel instance lúc đăng ký.

```mermaid
sequenceDiagram
    participant TG as Telegram
    participant CH as Channel Manager
    participant TI as Tenant Isolation
    participant AG as Agent Loop
    participant DB as PostgreSQL

    Note over CH: Channel instance load từ DB<br/>với tenant_id đã nướng vào

    TG->>CH: Incoming message (webhook)
    CH->>TI: Resolve tenant từ instance config
    TI->>AG: ctx với tenant_id + user_id
    AG->>DB: Query với WHERE tenant_id = $N
    AG-->>CH: Agent response
    CH-->>TG: Reply tới user
```

Mỗi channel instance lưu `tenant_id` của nó trong bảng `channel_instances`. Khi message đến, Channel Manager lookup instance config và inject tenant context — không cần header hoặc token.

| Channel | Protocol | Tenant Source |
|---------|----------|---------------|
| Telegram | Webhook | Instance config |
| Discord | WebSocket | Instance config |
| Zalo | Webhook | Instance config |
| Slack | Events API | Instance config |
| WhatsApp | Webhook | Instance config |
| Feishu/Lark | Webhook | Instance config |

---

## API Key Scope

API key dùng scope để kiểm soát access level:

| Scope | Role | Permission |
|-------|------|------------|
| `operator.admin` | admin | Full access — agent, config, API key, tenant |
| `operator.read` | viewer | Read-only — list agent, session, config |
| `operator.write` | operator | Read + write — chat, create session, manage agent |
| `operator.approvals` | operator | Approve/reject execution request |
| `operator.provision` | operator | Create tenant + manage tenant user |
| `operator.pairing` | operator | Manage device pairing |

Key với `["operator.read", "operator.write"]` được `operator` role. Key với `["operator.admin"]` được `admin` role.

---

## Per-Tenant Override

Tenant có thể customize môi trường của họ mà không ảnh hưởng tenant khác:

| Feature | Scope | Cách |
|---------|-------|------|
| **LLM Provider** | Per-tenant provider config | Mỗi tenant đăng ký API key + model riêng |
| **Builtin Tool** | Enable/disable + setting override per tenant | `builtin_tool_tenant_configs` (`enabled` + `settings` JSONB). 4-tier overlay (per-agent > tenant > global > hardcoded) resolve tại Execute time — xem `docs/03-tools-system.md` § 14 |
| **Skill** | Enable/disable per tenant | Bảng `skill_tenant_configs` |
| **MCP Server** | Per-tenant + per-user credential | Server-level shared, user-level override |
| **MCP Require User Credential** | Per-server setting | `settings.require_user_credentials` — force per-user API key |

MCP server hỗ trợ hai tier credential:
- **Server-level** (shared): cấu hình trong MCP server form, dùng bởi mọi user
- **User-level** (override): cấu hình qua "My Credentials", per-user API key merge tại runtime (user win khi key collision)

Khi `require_user_credentials` enabled, user không có personal credential không thể dùng MCP server đó.

---

## Bảo Mật

| Mối Quan Tâm | ManToManAgent Xử Lý Như Thế Nào |
|--------------|-----------------------------------|
| API key exposure | Key ở lại trong backend của bạn — không bao giờ gửi tới browser |
| Cross-tenant data access | Mọi SQL query bao gồm `WHERE tenant_id = $N` (fail-closed) |
| Event leakage | Server-side 3-mode filter: unscoped admin, scoped admin, regular user |
| Thiếu tenant context | Fail-closed: trả về error, không bao giờ unfiltered data |
| API key storage | Key hash bằng SHA-256 at rest; chỉ prefix hiển thị trong UI |
| Tenant impersonation | Tenant resolve từ API key binding, không phải client header |
| Cross-tenant privilege escalation trên global write | `store.IsMasterScope(ctx)` + `http.requireMasterScope(w, r)` guard mọi admin-gated write tới global table (`builtin_tools`, package management, config.*). Symmetric `requireTenantAdmin` guard tenant-scoped write. Predicate shared giữa HTTP + WS layer. Xem commit `b419f352` (Phase 1 WS) + `6d7473b5` (Phase 0b HTTP) |
| Privilege escalation | Role suy ra từ key scope, không phải client claim |
| Gateway token abuse | Chỉ configured owner ID có cross-tenant; người khác bị tenant-scoped |
| System config access | Config page restricted tới cross-tenant owner only |
| Logout isolation | Tenant scope cleared khỏi localStorage khi logout |
| Tenant access revocation | Proactive WS event + `TENANT_ACCESS_REVOKED` error force immediate UI logout |
| File URL security | HMAC-signed file token (`?ft=`) — không có gateway token trong URL |

---

## Tenant Data Model

```mermaid
erDiagram
    TENANTS ||--o{ TENANT_USERS : "member"
    TENANTS ||--o{ API_KEYS : "key"
    TENANTS ||--o{ AGENTS : "own"
    TENANTS ||--o{ SESSIONS : "own"
    TENANTS ||--o{ TEAMS : "own"
    TENANTS ||--o{ LLM_PROVIDERS : "configure"
    TENANTS ||--o{ MCP_SERVERS : "register"
    TENANTS ||--o{ SKILLS : "manage"

    TENANTS {
        uuid id PK
        string name
        string slug UK
        string status
        jsonb settings
    }

    TENANT_USERS {
        uuid tenant_id FK
        string user_id
        string role
    }

    API_KEYS {
        uuid id PK
        uuid tenant_id FK "NULL = system key"
        string owner_id
        text[] scopes
        boolean revoked
    }
```

40+ bảng mang `tenant_id` với NOT NULL constraint. Exception: `api_keys.tenant_id` nullable — NULL nghĩa là system-level cross-tenant key.

### v3 Tenant-Scoped Store

Store v3 mới (`evolution`, `vault`, `episodic`, `agent_links`) tất cả enforce tenant isolation:

| Store | Mục Đích | Tenant Scoping |
|-------|----------|----------------|
| `EvolutionMetrics` | Track agent improvement suggestion | `WHERE tenant_id = $N` |
| `EvolutionSuggestions` | Store LLM-generated optimization | `WHERE tenant_id = $N` |
| `Vault` | Persistent data storage cho agent | `WHERE tenant_id = $N` |
| `Episodic` | Episodic memory cho agent | `WHERE tenant_id = $N` |
| `AgentLink` | Delegation link giữa agent | `WHERE tenant_id = $N` |

Mọi v3 store theo cùng tenant isolation pattern — mọi query bao gồm `WHERE tenant_id = $N` ở SQL level.

**Master tenant** (UUID `0193a5b0-7000-7000-8000-000000000001`): Tất cả legacy/default data. Single-tenant deployment dùng độc quyền cái này.

---

## RBAC — 3 Role

Role-based access control cho WebSocket RPC method và HTTP API endpoint. Role theo thứ bậc: level cao hơn bao gồm mọi permission của level thấp hơn.

```mermaid
flowchart LR
    V["Viewer (level 1)<br/>Read-only access"] --> O["Operator (level 2)<br/>Read + Write"]
    O --> A["Admin (level 3)<br/>Full control"]
```

| Role | Permission Chính |
|------|------------------|
| Viewer | agents.list, config.get, sessions.list, health, status, skills.list |
| Operator | + chat.send, chat.abort, sessions.delete/reset, cron.*, skills.update |
| Admin | + config.apply/patch, agents.create/update/delete, channels.toggle, device.pair.approve/revoke |

### Access Check Flow

```mermaid
flowchart TD
    REQ["Method call"] --> S1["Bước 1: MethodRole(method)<br/>Xác định minimum required role"]
    S1 --> S2{"Bước 2: roleLevel(user) >= roleLevel(required)?"}
    S2 -->|Có| ALLOW["Cho phép"]
    S2 -->|Không| DENY["Từ chối"]
    S2 --> S3["Bước 3 (optional):<br/>CanAccessWithScopes() cho token<br/>có narrow scope restriction"]
```

**Role level:** Admin (3) > Operator (2) > Viewer (1).

---

## Mã Hóa

AES-256-GCM encryption cho secret lưu trong PostgreSQL. Key cung cấp qua `MANTOMANAGENT_ENCRYPTION_KEY` environment variable.

| Cái Gì Được Mã Hóa | Bảng | Cột |
|--------------------|------|-----|
| LLM provider API key | `llm_providers` | `api_key` |
| MCP server API key | `mcp_servers` | `api_key` |
| Custom tool env var | `custom_tools` | `env` |
| Credentialed CLI env var | `secure_cli_binaries`, `secure_cli_agent_grants`, `secure_cli_user_credentials`, `secure_cli_agent_credentials` | `encrypted_env` |

**Format**: `"aes-gcm:" + base64(12-byte nonce + ciphertext + GCM tag)`

Tương thích ngược: value không có prefix `aes-gcm:` được trả về plaintext (cho migration từ unencrypted data).

Credentialed CLI env entry có visibility kind riêng bên trong encrypted JSON blob khi `MANTOMANAGENT_ENCRYPTION_KEY` được cấu hình. `sensitive` entry được mask trong normal API/UI response và không bao giờ trả về raw trừ qua explicit audited grant reveal flow. `value` entry dùng cùng at-rest storage path nhưng được trả về cho authorized admin để operational review.

---

## Rate Limiting — Gateway + Tool

Bảo vệ ở hai level: gateway-wide (per user/IP) và tool-level (per session).

```mermaid
flowchart TD
    subgraph "Gateway Level"
        GW_REQ["Request"] --> GW_CHECK{"rate_limit_rpm > 0?"}
        GW_CHECK -->|Không| GW_PASS["Cho phép tất cả"]
        GW_CHECK -->|Có| GW_BUCKET{"Token bucket<br/>có capacity?"}
        GW_BUCKET -->|Available| GW_ALLOW["Cho phép + consume token"]
        GW_BUCKET -->|Exhausted| GW_REJECT["WS: INVALID_REQUEST error<br/>HTTP: 429 + Retry-After header"]
    end

    subgraph "Tool Level"
        TL_REQ["Tool call"] --> TL_CHECK{"Entry trong<br/>1 giờ qua?"}
        TL_CHECK -->|">= maxPerHour"| TL_REJECT["Error: rate limit exceeded"]
        TL_CHECK -->|"< maxPerHour"| TL_ALLOW["Record + cho phép"]
    end
```

| Level | Algorithm | Key | Burst | Cleanup |
|-------|-----------|-----|:-----:|---------|
| Gateway | Token bucket | user/IP | 5 | Mỗi 5 phút (inactive > 10 phút) |
| Tool | Sliding window | `agent:userID` | N/A | Manual `Cleanup()` |

Gateway rate limiting áp dụng cho cả WebSocket (`chat.send`) và HTTP (`/v1/chat/completions`) chat endpoint. Config: `gateway.rate_limit_rpm` (0 = disabled, giá trị dương = enabled).

---

## Biến Môi Trường

| Biến | Mặc Định | Mô Tả |
|------|----------|-------|
| `MANTOMANAGENT_OWNER_IDS` | `system` | User ID phân tách bằng dấu phẩy với cross-tenant access |
| `MANTOMANAGENT_LOG_LEVEL` | `info` | Log level: `debug`, `info`, `warn`, `error` |
| `MANTOMANAGENT_CONFIG` | `config.json5` | Path tới gateway config file |
| `MANTOMANAGENT_ENCRYPTION_KEY` | (không có) | AES-256-GCM key để mã hóa secret at rest |
| `MANTOMANAGENT_ALLOW_INSECURE_NO_AUTH` | `0` | Cho phép no-auth development mode (không an toàn, chỉ local) |

---

## Tham Khảo

| Document | Mô Tả |
|----------|-------|
| [09-security.md](./09-security.md) | Chi tiết 5 tầng bảo mật |
| [20-api-keys-auth.md](./20-api-keys-auth.md) | API key scope + auth flow |
| [06-store-data-model.md](./06-store-data-model.md) | 4-step `CanAccess` pipeline cho agent-level access control |
| [03-tools-system.md](./03-tools-system.md) | Builtin tool 4-tier overlay + per-tenant config |
| [19-websocket-rpc.md](./19-websocket-rpc.md) | WebSocket protocol v3 + event filtering |

---

**Nguyên Tắc Thiết Kế:**

1. **Fail-Closed** — Thiếu tenant context = error, không bao giờ trả về unfiltered data
2. **Defense-in-Depth** — 5 tầng bảo vệ độc lập, mỗi tầng hoạt động riêng biệt
3. **Explicit Over Implicit** — Tenant ID luôn rõ ràng trong context, không bao giờ infer từ state ẩn
4. **Least Privilege** — API key bound với tenant cụ thể, không có cross-tenant access trừ system-level key
5. **Auditability** — Mọi security event được log với `slog.Warn("security.*")`, bao gồm tenant_id và user_id

**Điểm Quan Trọng:**

- Tenant isolation enforce ở **SQL level** (`WHERE tenant_id = $N`), không phải application logic
- Credential không bao giờ nằm trong URL hoặc browser — dùng HMAC-signed token cho file access
- Mọi secret at rest được mã hóa AES-256-GCM với key riêng
- Cross-tenant admin write guard với `requireMasterScope` / `requireTenantAdmin` predicate
- Event filtering 3-mode (unscoped admin, scoped admin, regular user) ngăn event leakage
