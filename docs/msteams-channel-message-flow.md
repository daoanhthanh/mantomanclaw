# Luồng tin nhắn kênh Microsoft Teams

Tài liệu giải thích kiến trúc và luồng xử lý tin nhắn của kênh `msteams` (`internal/channels/msteams/`), áp dụng cho chat cá nhân (DM), group chat và thread trong channel.

## 1. Kiến trúc tổng quan

Bốn thành phần phối hợp, mỗi thành phần đảm nhiệm một việc riêng.

```mermaid
flowchart LR
    U["Người dùng Teams"] --> T["Teams client + service"]
    T -->|"Bot Framework activity + JWT"| AB["Azure Bot Service<br/>(mantomanclaw-teams-bot)"]
    AB -->|"POST /v1/channels/msteams/messages"| GW["mantomanagent gateway<br/>router, handlers, agent"]
    GW -->|"POST serviceUrl/v3/conversations/.../activities<br/>Bearer token"| AB
    AB --> T
    T --> U

    APP["Entra app registration<br/>app_id, tenant_id, cert/secret"] -.->|"danh tính bot"| AB
    APP -.->|"cấp token / xác thực"| GW
    MF["Teams app package<br/>(manifest zip)"] -.->|"khai báo scope: personal, team, groupchat"| T
```

| Thành phần | Vai trò |
|---|---|
| Teams app package (manifest) | Khai báo bot ID và các scope `personal`, `team`, `groupchat`. Chỉ khi có manifest, bot mới cài được vào DM, group chat và channel. |
| Azure Bot resource | Giữ messaging endpoint, bật Teams channel. Teams không gọi thẳng URL của bạn mà đi qua Azure Bot Service. |
| Entra app registration | Danh tính của bot: `app_id`, `tenant_id`, certificate hoặc client secret. |
| mantomanagent | Nhận request, xác thực JWT, áp dụng policy, chạy agent, gửi trả lời qua Connector API. |

Azure chỉ giữ danh tính và định tuyến. Server mantomanagent có thể chạy ở bất kỳ đâu có URL HTTPS công khai.

## 2. Luồng nhận tin (Teams đến agent)

```mermaid
sequenceDiagram
    autonumber
    actor User as Người dùng
    participant Teams
    participant Azure as Azure Bot Service
    participant Router as router.go
    participant JWT as jwt.go
    participant H as handlers.go
    participant DB as Store (msteams_conversations)
    participant Agent as Message bus + Agent

    User->>Teams: Gửi tin nhắn
    Teams->>Azure: Activity (ký JWT, aud = app_id)
    Azure->>Router: POST /v1/channels/msteams/messages
    Router->>Router: Giới hạn body 1 MiB, đọc serviceUrl
    alt Chưa có channel instance nào
        Router-->>Azure: 200, bỏ qua tin
    else Có channel instance
        Router->>JWT: Verify(Authorization, serviceUrl)
        JWT-->>Router: RS256, issuer, audience, JWKS, serviceUrl khớp
        alt JWT không hợp lệ với mọi instance
            Router-->>Azure: 401
        else JWT hợp lệ
            Router-->>Azure: 200 ngay (ack trước)
            Router->>H: goroutine: handleActivity(body)
        end
    end
    H->>H: dispatchActivity theo act.Type
    H->>DB: upsertConvRef (lưu serviceUrl + conversation ID)
    H->>H: Kiểm tra tenant, dedup, mention, policy
    H->>Agent: HandleAuthorizedMessageMedia
```

Router trả `200` trước khi xử lý để Teams không gửi lại khi agent chạy chậm.

## 3. Xử lý trong `handleMessage`

```mermaid
flowchart TD
    A["Activity type = message"] --> B{"Tenant khớp<br/>tenant_id?"}
    B -- Không --> X1["Bỏ qua<br/>log security.msteams.tenant_mismatch"]
    B -- Có --> C{"Tin của chính bot?"}
    C -- Có --> X2["Bỏ qua"]
    C -- Không --> D{"Activity ID<br/>đã thấy (dedup)?"}
    D -- Có --> X3["Bỏ qua"]
    D -- Không --> E["upsertConvRef:<br/>lưu serviceUrl + conversation ID"]
    E --> F{"conversationType"}
    F -- personal --> G1["peerKind = direct<br/>chatID = conversation ID"]
    F -- groupChat --> G2["peerKind = group<br/>chatID = conversation ID"]
    F -- channel --> G3["peerKind = group<br/>chatID = teamsChannelId:thread:rootMessageId"]
    G1 --> H["Bỏ @mention của bot, chuẩn hóa HTML"]
    G2 --> H
    G3 --> H
    H --> I{"Group/channel và<br/>require_mention bật?"}
    I -- Có --> J{"Bot được @mention<br/>hoặc còn trong thread_ttl?"}
    J -- Không --> X4["Chỉ ghi history, không trả lời"]
    J -- Có --> K
    I -- Không --> K{"Policy"}
    K -- "direct: dm_policy" --> L["allow / deny / cần pairing"]
    K -- "group: group_policy" --> L
    L -- deny --> X5["Bỏ qua"]
    L -- "cần pairing" --> X6["Gửi mã pairing, dừng"]
    L -- allow --> M["Đánh dấu participation, thu thập contact"]
    M --> N["Đưa vào message bus, agent pipeline"]
```

Quy tắc theo loại hội thoại:

| Scope Teams | `conversationType` | Khóa session (`chatID`) | Policy |
|---|---|---|---|
| Chat cá nhân | `personal` | conversation ID | `dm_policy` |
| Group chat | `groupChat` | conversation ID | `group_policy` |
| Thread trong channel | `channel` | `<teamsChannelId>:thread:<rootMessageId>` | `group_policy` |

- Mỗi thread trong channel có session riêng nhờ khóa `:thread:`.
- `group_policy` mặc định là `pairing`: người dùng mới trong group phải được pairing trước khi bot trả lời.
- `require_mention` mặc định tắt. Khi bật, bot chỉ trả lời tin group/channel có @mention, và vẫn tiếp tục trả lời trong `thread_ttl` (mặc định 10 phút) sau lần trả lời gần nhất.

## 4. Luồng gửi tin (agent đến Teams)

```mermaid
sequenceDiagram
    autonumber
    participant Agent as Agent
    participant Send as send.go
    participant DB as Store
    participant Conn as connector.go
    participant Tok as token.go
    participant AAD as login.microsoftonline.com
    participant BF as Bot Framework (serviceUrl)
    actor User as Người dùng

    Agent->>Send: Send(OutboundMessage)
    Send->>Send: Tách ChatID thành conversation ID + thread root
    Send->>DB: Tra serviceUrl theo conversation ID
    alt Không có serviceUrl
        DB-->>Send: rỗng
        Send-->>Agent: lỗi "no service URL for conversation"
    else Có serviceUrl
        DB-->>Send: serviceUrl
        Send->>Send: Thread: conversation ID + #59;messageid=root, replyToId = root
        Send->>Send: Chia chunk tối đa 20.000 ký tự
        Send->>Conn: postActivity / putActivity
        Conn->>Tok: Token(scope api.botframework.com/.default)
        Tok->>AAD: client_credentials (secret hoặc cert assertion)
        AAD-->>Tok: access_token (được cache)
        Conn->>BF: POST serviceUrl/v3/conversations/id/activities
        BF-->>User: Tin trả lời hiển thị trong Teams
    end
```

Chi tiết:

- Chunk đầu tiên sửa (PUT) tin placeholder "Thinking..." nếu có, nếu không thì gửi mới. Các chunk còn lại và media được gửi mới.
- `connector.go` thử lại tối đa 3 lần, tôn trọng `Retry-After` khi gặp `429`, không thử lại các lỗi `4xx` khác.
- Với cert auth, token được lấy bằng client assertion RS256 có header `x5t#S256`.

## 5. Hai chiều xác thực độc lập

```mermaid
flowchart LR
    MS["Microsoft<br/>Bot Framework"] -->|"1. JWT ký bởi Microsoft<br/>server kiểm tra bằng JWKS (jwt.go)"| S["mantomanagent"]
    S -->|"2. OAuth token cho app_id<br/>lấy từ Entra (token.go)"| MS
```

- **Nhận:** Microsoft chứng minh với server. Server xác minh chữ ký, issuer `https://api.botframework.com`, audience `app_id` và `serviceUrl`.
- **Gửi:** server chứng minh với Microsoft bằng token `client_credentials` của `app_id`.

## 6. Lưu ý quan trọng

- **Bot không thể nhắn trước.** `serviceUrl` và conversation ID chỉ được lưu khi có activity đến (`upsertConvRef`) hoặc bot vừa được thêm vào (`conversationUpdate`). Mỗi hội thoại cần ít nhất một tin đến trước khi gửi đi.
- **Khóa tenant.** Activity từ tenant khác `tenant_id` bị bỏ qua. Phù hợp bot nội bộ single-tenant.
- **Endpoint phải là HTTPS công khai.** Teams không gọi được localhost.
- **Mã trả `401` với request không có JWT là bình thường.** Nó chứng tỏ endpoint đang hoạt động và có ít nhất một channel instance đã đăng ký.
- **Chưa kiểm chứng trong repo:** message bus và agent pipeline thuộc framework kênh chung và không nằm trong tài liệu này. Nơi tạo placeholder "Thinking..." cũng chưa xác định, có thể là `stream.go`.
