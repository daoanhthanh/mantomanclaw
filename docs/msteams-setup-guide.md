# Hướng dẫn tạo Teams bot + Azure Bot Service và kết nối với mantomanagent

Dành cho bot **single-tenant, dùng nội bộ** (DM, group chat, thread trong channel, đọc file SharePoint). Kiến trúc và luồng tin nhắn: xem `docs/msteams-channel-message-flow.md`.

```mermaid
flowchart LR
    A["1. Entra app registration<br/>+ certificate"] --> B["2. Azure Bot resource<br/>+ messaging endpoint"]
    B --> C["3. Teams app package<br/>(sideload / admin upload)"]
    A --> D["4. Graph Sites.Selected<br/>(đọc SharePoint)"]
    A --> E["5. Channel instance<br/>trong mantomanagent"]
    B --> E
    D --> E
    E --> F["6. Test: DM, group, thread"]
```

## 0. Chuẩn bị

- Gateway có URL HTTPS công khai, ví dụ `https://agent.example.com`. Teams không gọi được localhost.
- Quyền: Application Administrator (hoặc Global Admin) để cấp admin consent, và quyền tạo resource trong Azure subscription.
- Ghi lại các giá trị sẽ dùng ở bước 5:

| Giá trị | Lấy ở đâu |
|---|---|
| `app_id` | Entra app registration, Application (client) ID |
| `tenant_id` | Entra app registration, Directory (tenant) ID |
| certificate + private key | Bước 1 |

## 1. Entra app registration

1. [Azure Portal](https://portal.azure.com), **Microsoft Entra ID**, [App registrations](https://portal.azure.com/#view/Microsoft_AAD_RegisteredApps/ApplicationsListBlade), **New registration**.
2. Name: `mantomanclaw-teams-bot`. Supported account types: **Single tenant only**. Không cần redirect URI. Bấm **Register**.
3. Ghi lại **Application (client) ID** và **Directory (tenant) ID**.

### Certificate (không hết hạn)

Tạo cert tự ký 100 năm trên máy bạn:

```bash
openssl req -x509 -newkey rsa:2048 -nodes -days 36500 \
  -keyout ms-teams-bot-private.key -out ms-teams-bot-cert.pem \
  -subj "/CN=mantomanclaw-teams-bot"
```

1. Mở app trong [App registrations](https://portal.azure.com/#view/Microsoft_AAD_RegisteredApps/ApplicationsListBlade), **Certificates & secrets**, tab **Certificates**, **Upload certificate**, chọn `ms-teams-bot-cert.pem`.
2. Giữ `ms-teams-bot-private.key` bí mật. Không commit 2 file này vào git (hiện chúng đang untracked trong repo, thêm vào `.gitignore` hoặc chuyển ra ngoài repo).

Có thể dùng **client secret** thay cho cert (đơn giản hơn nhưng hết hạn tối đa 24 tháng). Chỉ cần một trong hai.

## 2. Azure Bot resource

1. [Azure Portal](https://portal.azure.com), [**Create a resource**](https://portal.azure.com/#create/Microsoft.AzureBot) (trực tiếp tới form **Azure Bot**), **Create**.
2. Bot handle: `mantomanclaw-teams-bot`. Chọn subscription và resource group. Pricing: **F0** (free) đủ cho nội bộ, hoặc S1.
3. **Type of App**: **Single Tenant**. **Creation type**: **Use existing app registration**, nhập `app_id` và `tenant_id` ở bước 1.
4. Sau khi tạo: vào bot, **Settings**, **Configuration**, đặt **Messaging endpoint**:

   ```
   https://<domain-công-khai>/v1/channels/msteams/messages
   ```

5. **Channels**, thêm **Microsoft Teams**, chấp nhận điều khoản, **Apply**.

Lưu ý: nút **Test in Web Chat** của Azure không phản ánh đúng luồng Teams (có thể báo "no eligible channel messages"). Hãy test trực tiếp trong Teams ở bước 6.

## 3. Teams app package (sideload)

Bot chỉ dùng được trong Teams khi có app package. Test cá nhân thì **sideload**. Cho cả tổ chức thì admin **upload zip thẳng ở Teams admin center** (xem "Triển khai cho cả tổ chức" bên dưới). Không publish qua [Developer Portal](https://dev.teams.microsoft.com/apps): nó hay kẹt pending hoặc báo "Failed to create publish request".

Tạo thư mục gồm 3 file: `manifest.json`, `color.png` (192x192), `outline.png` (32x32, nền trong). Nén thành `teams-app.zip` (3 file nằm ở gốc zip, không nằm trong thư mục con).

```json
{
  "$schema": "https://developer.microsoft.com/json-schemas/teams/v1.17/MicrosoftTeams.schema.json",
  "manifestVersion": "1.17",
  "version": "1.0.0",
  "id": "<app_id>",
  "developer": {
    "name": "THANHDAO LIMITED",
    "websiteUrl": "https://agent.example.com",
    "privacyUrl": "https://agent.example.com/privacy",
    "termsOfUseUrl": "https://agent.example.com/terms"
  },
  "name": { "short": "ManToMan Agent", "full": "ManToMan Agent" },
  "description": { "short": "AI agent", "full": "AI agent của THANHDAO LIMITED" },
  "icons": { "color": "color.png", "outline": "outline.png" },
  "accentColor": "#4F6BED",
  "bots": [
    {
      "botId": "<app_id>",
      "scopes": ["personal", "team", "groupChat"],
      "supportsFiles": false,
      "isNotificationOnly": false
    }
  ],
  "permissions": ["identity", "messageTeamMembers"],
  "validDomains": ["agent.example.com"]
}
```

- `id` và `botId` đều là `app_id`.
- `scopes` quyết định bot dùng được ở đâu: `personal` (DM), `groupChat`, `team` (channel).
- Thông báo "Update your manifest to include `supportsChannelFeatures=tier1`" chỉ là gợi ý cho tính năng channel mới, cần manifest từ 1.24. Không bắt buộc cho bot này.
- Mặc định Teams chỉ gửi cho bot các tin có @mention trong group/channel. Muốn bot nhận mọi tin trong channel/chat cần khai báo RSC permission trong manifest (`authorization.permissions.resourceSpecific`). Việc này chưa được kiểm chứng trong repo, nên cứ dùng @mention trước.

### Cài đặt cá nhân (sideload, để test)

Trong Teams: **Apps**, **Manage your apps**, **Upload an app**, **Upload a custom app**. Chỉ người upload thấy app. Cần policy **Upload custom apps** bật trong [Teams admin center](https://admin.teams.microsoft.com) ([Setup policies](https://admin.teams.microsoft.com/policies/app-setup), Global).

### Triển khai cho cả tổ chức

Cần role Teams Administrator hoặc Global Administrator.

1. [Teams admin center](https://admin.teams.microsoft.com), **Teams apps**, [**Manage apps**](https://admin.teams.microsoft.com/policies/manage-apps), **Actions**, **Upload new app**, chọn zip. (Zip cũng có thể tải từ [Developer Portal](https://dev.teams.microsoft.com/apps): **Apps**, chọn app, **Download app package**.)
2. Mở app vừa upload, tab **Users and groups**: **Available to** = **Everyone**, trạng thái **Allowed**.
3. Chờ app hiện cho người dùng. Microsoft ghi "vài giờ" cho app upload qua admin center.
4. Người dùng: Teams, **Apps**, **Built for your org**, **ManToMan Agent**, **Add** để nhắn DM. Group chat/channel: gõ `@ManToMan`, bấm **Add**.
5. Đặt `dm_policy` và `group_policy` thành `open` ở bước 5 để mọi người trong tenant dùng được (xem bảng ở đó).
6. Tuỳ chọn, pin sẵn cho người dùng: **Teams apps**, [**Setup policies**](https://admin.teams.microsoft.com/policies/app-setup), mở policy áp dụng, **Installed apps** và **Pinned apps**, **Add apps**, chọn ManToMan Agent, **Save**.

Entry cũ kẹt "Pending approval" ở Manage apps: xóa đi, hoặc bỏ qua.

Nếu **Upload new app** xám hoặc ẩn, kiểm tra theo thứ tự: role của tài khoản ([Microsoft Entra admin center](https://entra.microsoft.com), Roles and administrators), có đang tìm trong menu **Actions** không, thử trình duyệt khác hoặc ẩn danh, policy custom app của tenant và đợi propagate. Chưa có tài liệu xác nhận license Teams Essentials chặn upload này, nên đừng nâng gói trước khi thử hết các bước trên.

## 4. Đọc file SharePoint (Graph `Sites.Selected`)

Bot tải file qua Microsoft Graph, chỉ cho các site bạn cấp quyền. Cần cả hai lớp: quyền ở Entra và allowlist trong cấu hình mantomanagent.

1. Mở app trong [App registrations](https://portal.azure.com/#view/Microsoft_AAD_RegisteredApps/ApplicationsListBlade), **API permissions**, **Add a permission**, **Microsoft Graph**, **Application permissions**, chọn **Sites.Selected**.
2. Bấm **Grant admin consent for <tenant>**.
3. Lấy site ID. Ví dụ với `https://contoso.sharepoint.com/sites/Team`:

   ```
   GET https://graph.microsoft.com/v1.0/sites/contoso.sharepoint.com:/sites/Team
   ```

   Trường `id` có dạng `hostname,siteCollectionId,webId`. Có thể chạy trong [Graph Explorer](https://developer.microsoft.com/graph/graph-explorer).
4. Cấp quyền đọc cho app trên site đó (cần token có `Sites.FullControl.All`, ví dụ admin chạy trong [Graph Explorer](https://developer.microsoft.com/graph/graph-explorer)):

   ```
   POST https://graph.microsoft.com/v1.0/sites/{site-id}/permissions
   {
     "roles": ["read"],
     "grantedToIdentities": [
       { "application": { "id": "<app_id>", "displayName": "mantomanclaw-teams-bot" } }
     ]
   }
   ```

5. Điền `allowed_site_ids` ở bước 5. Chấp nhận dạng đầy đủ `hostname,siteCollectionId,webId` hoặc chỉ `siteCollectionId` / `webId` (code so khớp từng thành phần).

Giới hạn:

| Mục | Giá trị |
|---|---|
| Kích thước file | tối đa 20 MiB |
| Số file mỗi tin | tối đa 5 (file đính kèm + link SharePoint) |
| Host cho phép | `graph.microsoft.com`, `*.sharepoint.com`, và `allowed_hosts` |
| OneDrive cá nhân (`*-my.sharepoint.com`) | **Bị bỏ qua**, bot không có quyền. Hãy để file ở team site |
| Site chưa có trong `allowed_site_ids` | Từ chối, log `site "..." not granted to the bot` |

Cách agent nhận file: file đính kèm Teams hoặc link `https://*.sharepoint.com/...` dán trong tin nhắn sẽ được tải về, rồi gửi cho agent như media của tin nhắn đó. Chỉ tải sau khi qua policy (`dm_policy` / `group_policy`), nên người bị từ chối không kích hoạt tải file.

Ghi chú: Microsoft yêu cầu cert thay cho secret với một số luồng tải file từ 2027-03-01. Thông tin này nằm trong comment của `config.go`, chưa kiểm chứng. Dùng cert ở bước 1 là an toàn nhất.

## 5. Tạo channel instance trong mantomanagent

Vào Web UI, **Channels**, thêm instance loại **Microsoft Teams** (hoặc qua CLI `mantomanagent channels` / setup wizard).

**Credentials (JSON)**, dùng cert:

```json
{
  "app_id": "<app_id>",
  "tenant_id": "<tenant_id>",
  "certificate_pem": "-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----\n",
  "private_key_pem": "-----BEGIN PRIVATE KEY-----\n...\n-----END PRIVATE KEY-----\n"
}
```

Hoặc dùng secret: `{"app_id": "...", "tenant_id": "...", "client_secret": "..."}`.

Mẹo tạo chuỗi PEM một dòng: `awk 'NF {printf "%s\\n", $0}' ms-teams-bot-cert.pem`.

**Config (JSON hoặc form UI).** Lưu ý: backend coi `require_mention` bỏ trống là *tắt*. Form UI mặc định bật, nhưng nếu tạo bằng JSON/API thì phải đặt `true` tường minh.

```json
{
  "dm_policy": "pairing",
  "group_policy": "pairing",
  "require_mention": true,
  "thread_ttl": 0,
  "history_limit": 50,
  "allowed_site_ids": ["<siteCollectionId hoặc composite site id>"],
  "allowed_hosts": []
}
```

| Trường | Ý nghĩa |
|---|---|
| `dm_policy` | Chính sách DM: `pairing` (mặc định), `open`, `allowlist`, `disabled` |
| `group_policy` | Chính sách group/channel, mặc định `pairing` |
| `allow_from` | Danh sách AAD object ID hoặc UPN được phép |
| `require_mention` | Group/channel chỉ trả lời khi @mention, hoặc trong `thread_ttl` sau lần trả lời gần nhất |
| `thread_ttl` | **Phút** (mặc định 10). `0` là luôn cần @mention. Muốn bot chỉ chạy khi được @mention thì đặt `0` |
| `allowed_site_ids` | Site SharePoint bot được đọc (bước 4) |

Triển khai cho cả tổ chức: đặt `dm_policy` và `group_policy` là `open`. Bot vẫn chỉ nhận tin từ `tenant_id` đã cấu hình; người ngoài tenant bị chặn (log `security.msteams.tenant_mismatch`) dù policy là `open`.

Sau khi lưu, channel khởi động và lấy thử token Bot Connector. Nếu sai `app_id`/`tenant_id`/cert, channel báo `token acquisition failed` ngay (xem trạng thái channel trong UI hoặc log `msteams channel started`).

## 6. Kiểm tra theo thứ tự

1. **Endpoint sống**: `curl -i -X POST https://<domain>/v1/channels/msteams/messages -d '{}'` phải trả `401`. `401` nghĩa là endpoint hoạt động và đã có channel instance (không có instance thì trả `200`).
2. **DM**: trong Teams mở app, nhắn tin. Với `dm_policy=pairing`, bot trả mã pairing, duyệt mã trong UI/CLI rồi nhắn lại.
3. **Group chat**: thêm bot vào group, @mention bot. Duyệt pairing nhóm nếu `group_policy=pairing`.
4. **Thread trong channel**: thêm app vào team (scope `team`), @mention bot trong một post. Mỗi thread có session riêng.
5. **SharePoint**: dán link file trong team site đã cấp quyền, hoặc đính kèm file, rồi hỏi agent về nội dung.

Log đáng chú ý:

| Log | Nghĩa |
|---|---|
| `security.msteams.tenant_mismatch` | Activity từ tenant khác `tenant_id`, bị bỏ qua |
| `msteams: no service URL for conversation` | Bot chưa nhận tin nào trong hội thoại đó (bot không nhắn trước được) |
| `msteams: attachment download failed` / `sharepoint link download failed` | Xem lỗi kèm theo: `site ... not granted`, `host not allowed`, `HTTP 403` (thiếu grant ở bước 4) |

## 7. Xử lý sự cố nhanh

| Triệu chứng | Nguyên nhân thường gặp |
|---|---|
| "This app is not available" trong Teams | App chưa sideload, hoặc dùng entry org catalog bị pending. Cài bằng bước 3 |
| App đã upload nhưng người dùng không thấy ở **Built for your org** | Chưa hết thời gian propagate, **Available to** chưa là Everyone, hoặc App permission policy của user chặn custom app. Thoát hẳn Teams rồi mở lại |
| Bot không trả lời, log không có gì | Messaging endpoint sai/trống, hoặc chưa bật Teams channel ở Azure Bot |
| Endpoint trả `401` với tin thật từ Teams | `app_id` trong instance khác với app của Azure Bot, hoặc `serviceUrl` không khớp JWT |
| Có JWT hợp lệ nhưng không phản hồi | Tin bị dedup, tenant mismatch, hoặc policy `pairing` chưa duyệt |
| Group/channel im lặng | Chưa @mention bot (và hết `thread_ttl`) |
| Không đọc được file SharePoint | Thiếu admin consent `Sites.Selected`, chưa grant site (bước 4.4), site ID chưa có trong `allowed_site_ids`, hoặc file ở OneDrive cá nhân |
