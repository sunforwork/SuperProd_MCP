# Super Productivity MCP

为 Super Productivity 提供 MCP 编辑能力的自托管 WebDAV 服务。手机、电脑和 ChatGPT 操作同一份同步数据；每个用户拥有独立的数据库、OAuth 凭据、WebDAV 密码和同步加密密码。

感谢 [LINUX DO](https://linux.do/) 社区及各位佬友的支持。

**推荐安装：宝塔原生 Go 项目。** 上传预编译二进制，通过宝塔启动，在网页完成首次安装。运行时不需要 Node.js、Redis、外置数据库或 Docker。

当前版本 **v2.3**：新增首次安装向导、自定义管理员用户名和中文部署流程；保留 v2.2 的完整业务 JSON 草稿、批准后状态替换、命名备份与回档。

## 功能与兼容范围

- WebDAV 同步、OAuth 认证的远程 MCP、`/admin` 用户与凭据管理。
- 已实现的任务、项目、标签、笔记、计划、工时与归档工具；完整业务 JSON 编辑和命名恢复点。
- 一个进程、一个 HTTP 端口、多用户隔离；域名、HTTPS 和进程守护交给宝塔。
- 固定客户端基准：`71eb7780bcf5d6b1dfdcd39a8a8265547d040760`，目标写入格式为 **v2 同步文件、schema v4**。
- **完整客户端 1:1 行为复刻仍在开发**。请以 `get_capabilities` 的实现与验证信息为准；开启写入不等于全部客户端操作已经实现。
- 首次使用请在客户端关闭“精细同步（实验性）”。v3 读取有兼容限制，v3 写入和迁移不属于当前发布能力。
- 新安装默认允许已实现的 MCP 写入；安装时可选择只读。已有安装保留原写入设置。自由编辑与回档仍要求一次完整差异批准。

## 一、下载安装包与检查校验值

从本仓库 GitHub Releases 下载用户需要的版本；没有 Release 时表示该版本尚未公开发布。使用命令检查服务器架构：

```sh
uname -m
```

| 服务器架构 | 安装包 |
| --- | --- |
| `x86_64`（大多数云服务器） | `super-productivity-mcp-linux-amd64.zip` |
| `aarch64` / ARM64 | `super-productivity-mcp-linux-arm64.zip` |

同时下载该版本的 `SHA256SUMS.txt`，上传到与 ZIP 相同的目录，在宝塔终端核对：

```sh
sha256sum --ignore-missing -c SHA256SUMS.txt
```

对应 ZIP 应显示 `OK`。不要混用不同版本的程序、插件和校验值。

## 二、上传程序与准备权限

用宝塔文件管理器把安装包解压到 `/www/wwwroot/super-productivity-mcp`，保证程序实际位于：

```text
/www/wwwroot/super-productivity-mcp/spmcp
```

若解压后多出一层目录，移动程序和配套文件到上述位置。在宝塔终端执行：

```sh
chmod 755 /www/wwwroot/super-productivity-mcp/spmcp
install -d -o www -g www -m 700 /www/server/spmcp-data
```

数据目录必须属于运行用户 `www`，**全新安装时应为空**。不用手工创建 `config.json` 或密码文件，也不用先执行 `init`、`enable-oauth`、`enable-admin`。

程序目录与数据目录分开；升级程序时不覆盖数据。路径可按需要调整，但后续启动命令必须指向同一个配置位置。

## 三、宝塔添加 Go 项目

进入宝塔“网站 → Go 项目 → 添加 Go 项目”，按表填写：

| 字段 | 填写内容 |
| --- | --- |
| 项目执行文件 | `/www/wwwroot/super-productivity-mcp/spmcp` |
| 项目名称 | `super-productivity-mcp`，可自定义 |
| 项目端口 | `8787` |
| 放行端口 | **不勾选**，由同机 Nginx 访问回环端口 |
| 执行命令 | 下面的完整命令 |
| 环境变量 | 选择“无” |
| 运行用户 | `www` |
| 开机启动 | 勾选 |
| 项目备注 | 自行填写 |
| 绑定域名 | 自己解析到本服务器的域名；已有独立反代站点时，由该站点绑定 |

执行命令，首次安装与安装后始终相同：

```sh
/www/wwwroot/super-productivity-mcp/spmcp serve --config /www/server/spmcp-data/config.json
```

保持前台运行，让宝塔管理启停；不用 `nohup`、`&` 或额外 systemd 守护。

默认内部监听 `127.0.0.1:8787`。首次安装需要其他端口时，启动命令增加 `--listen 127.0.0.1:新的端口`，宝塔项目端口与所有反代目标同时修改。安装完成后监听地址以生成的配置为准，启动参数不会覆盖已有配置。

字段含义可参照 [宝塔官方 Go 项目教程](https://www.bt.cn/bbs/thread-93034-1-1.html)。

## 四、宝塔配置域名、SSL 和反向代理

先在宝塔完成域名绑定、可信 SSL 证书和 HTTP 跳转 HTTPS。Go 程序只提供内部 HTTP，不申请证书，也不监听公网 443。

代理目标：

```text
http://127.0.0.1:8787
```

打开宝塔生成的网站 Nginx 配置，**仅在现有 `server { ... }` 内调整反向代理区域**：

- 保留原有 `listen`、`server_name`、`root`、SSL、证书验证目录、`include`、日志和监控配置。
- 保留 SSL 区域里的 `#error_page 404/404.html;` 保护注释。不要全选替换整个配置文件。
- 用下面片段替换已有的代理 `location /`，不要重复添加相同的 location。
- 不要把这些 location 独立粘到被 Nginx 在 `http` 层直接加载的站点文件中，否则会报 `location directive is not allowed here`。
- 已有相同路径规则时合并或替换；不要同时保留两份。

```nginx
location ^~ /.well-known/oauth- {
    proxy_pass http://127.0.0.1:8787;
    proxy_set_header Host $proxy_host;
    proxy_set_header Connection "";
    proxy_cache off;
}
location = /.well-known/openid-configuration {
    proxy_pass http://127.0.0.1:8787;
    proxy_set_header Host $proxy_host;
    proxy_cache off;
}
location ^~ /oauth/ {
    proxy_pass http://127.0.0.1:8787;
    proxy_http_version 1.1;
    proxy_set_header Host $proxy_host;
    proxy_set_header Authorization $http_authorization;
    proxy_set_header Connection "";
    proxy_buffering off;
    proxy_cache off;
    client_max_body_size 16k;
    access_log off;
}
location = /mcp {
    proxy_pass http://127.0.0.1:8787;
    proxy_http_version 1.1;
    proxy_set_header Host $proxy_host;
    proxy_set_header Authorization $http_authorization;
    proxy_set_header Connection "";
    proxy_buffering off;
    proxy_cache off;
    proxy_read_timeout 65s;
    client_max_body_size 2m;
    access_log off;
}
location ^~ /mcp/ { return 404; }
location ^~ /super-productivity/ {
    proxy_pass http://127.0.0.1:8787;
    proxy_http_version 1.1;
    proxy_set_header Host $proxy_host;
    proxy_set_header Authorization $http_authorization;
    proxy_set_header Connection "";
    proxy_buffering off;
    proxy_cache off;
    proxy_read_timeout 65s;
    client_max_body_size 64m;
}
location ^~ /setup {
    proxy_pass http://127.0.0.1:8787;
    proxy_set_header Host $proxy_host;
    proxy_set_header Connection "";
    proxy_buffering off;
    proxy_cache off;
    client_max_body_size 16k;
}
location ^~ /admin {
    proxy_pass http://127.0.0.1:8787;
    proxy_set_header Host $proxy_host;
    proxy_set_header Connection "";
    proxy_buffering off;
    proxy_cache off;
    client_max_body_size 16k;
}
location ^~ /assets/ui/ {
    proxy_pass http://127.0.0.1:8787;
    proxy_set_header Host $proxy_host;
    proxy_cache off;
}
location / {
    proxy_pass http://127.0.0.1:8787;
    proxy_http_version 1.1;
    proxy_set_header Host $proxy_host;
    proxy_set_header Authorization $http_authorization;
    proxy_set_header Connection "";
    proxy_buffering off;
    proxy_cache off;
    proxy_read_timeout 65s;
    client_max_body_size 64m;
}
```

`Host` 使用 `$proxy_host`，让程序收到内部地址；不需要把公网域名填入内部 Host 白名单，也不要用通配符关闭校验。浏览器的 `Origin` 正常透传，安装和 OAuth 使用真实 HTTPS 地址校验。

通过宝塔保存、检查并重载 Nginx。检查本地服务：

```sh
curl http://127.0.0.1:8787/healthz
```

`installation: required` 表示进程正在等待安装；`installation: ready` 表示已安装。`releaseReady: false` 表示完整客户端兼容性尚未完成，不代表进程故障。

## 五、网页首次安装

1. 打开宝塔 Go 项目的日志，找到“首次安装”一行中的安装码；不要把它发给其他人。
2. 访问 `https://自己的域名/setup`，输入安装码。安装码 30 分钟有效，只能兑换一次。
3. 检查页面上的运行版本、内部监听、数据目录、写入权限和 SQLite 状态。
4. 确认公网 HTTPS 基地址，使用当前安装网站地址，不包含 `/mcp` 等路径。
5. 填写自定义管理员用户名；生成或设置管理员密码，保存到密码管理器。管理员密码至少 12 字节。
6. 填写首个业务用户的用户名及显示名称。管理员与业务用户名不能相同；用户名区分大小写，最多 64 字节，不能包含冒号或控制字符。
7. 分别设置 WebDAV 密码、OAuth 登录密码和同步加密密码。每项可随机生成 24 位，也可自己填写。
8. OAuth Client ID 自动生成；Client Secret 默认生成 256 位随机密钥，也可自己设置。
9. 默认勾选“允许 ChatGPT 使用已实现的 MCP 写入工具”，可改为只读。
10. 检查摘要并完成安装，复制配置或主动下载完整凭据清单，再进入 `/admin` 管理面板。

管理员密码只保存慢哈希，安装后不能查看原文。下载的 `spmcp-credentials.json` 包含密码，应放在私密位置。业务连接凭据可在管理面板查看和复制；程序不会把它们写入普通日志或浏览器持久化存储。

安装完成后不需要重启项目；安装接口关闭，再访问 `/setup` 会转到管理登录。安装中断或安装会话闲置 30 分钟失效时，重启尚未安装的项目获取新码。已安装系统不会因重启重新开放安装。

### 全新数据和已有客户端数据

- **全新使用**：可以生成新的同步加密密码，在手机、电脑使用同一密码。
- **已有客户端数据**：填写原客户端的同步加密密码，不能用新随机密码替代。先在客户端保存完整备份，再把 WebDAV 改为本服务，并按客户端的首次同步选项上传当前数据。不要选择用空云端覆盖本地已有数据。

安装后的业务空间为空，显示“等待客户端首次上传”；此时没有业务文件可验证。首次上传后，在管理面板用户详情中点击“检查上传与同步密码”，确认同步文件能解密并读取。安装向导本轮不提供网页备份导入。

## 六、手机和电脑客户端连接

在 Super Productivity 的同步设置中选择 WebDAV，填写安装完成页或管理面板提供的值：

| 客户端设置 | 内容 |
| --- | --- |
| 基础 URL | `https://自己的域名`，直接使用 HTTPS |
| 用户名 | 该业务用户的用户名 |
| 密码 | **WebDAV 密码** |
| 同步文件夹路径 | `super-productivity` |
| 加密密码 | **同步加密密码** |
| 精细同步（实验性） | 当前写入范围请关闭 |

不要在基础 URL 后重复添加同步文件夹，也不要填写 `/mcp`。先做连接测试，再执行首次上传。其他设备连接相同账号后，从云端同步该账号数据。每个业务用户只能访问自己的空间。

## 七、ChatGPT OAuth 连接

在 ChatGPT 的自定义 MCP／插件连接入口填写：

| 设置 | 内容 |
| --- | --- |
| 名称 | 自行命名 |
| MCP URL | `https://自己的域名/mcp` |
| 身份验证 | OAuth |
| 客户端注册 | 自定义 OAuth 客户端 |
| Client ID / Client Secret | 该业务用户的独立凭据 |
| Token 端点身份验证方法 | `client_secret_basic` |
| 默认权限范围 | `mcp` |
| OIDC | 不开启，本服务不是 OIDC 身份提供方 |

OAuth 地址可自动发现；需要手动填写时：

```text
授权地址：https://自己的域名/oauth/authorize
Token 地址：https://自己的域名/oauth/token
授权服务器基础地址：https://自己的域名
资源：https://自己的域名/mcp
回调地址：https://chatgpt.com/connector_platform_oauth_redirect
```

连接时跳转到本服务页面，用**业务用户名和 OAuth 登录密码**授权，不是管理员账号或 WebDAV 密码。接口检查通过只表示服务入口就绪，实际授权和工具调用必须在 ChatGPT 中验证。

服务器无需为了这条授权码流程主动访问 ChatGPT；浏览器完成回调，ChatGPT 请求本服务的 Token 和 MCP 地址。公网 HTTPS、OAuth 发现与反代必须能被 ChatGPT 访问。

需要插件 ZIP 时，可在宝塔终端使用实际域名生成无凭据包：

```sh
/www/wwwroot/super-productivity-mcp/spmcp plugin-package --mcp-url https://自己的域名/mcp --output /tmp/super-productivity-cloud-plugin.zip
```

该输出路径必须不存在且可写，命令不会覆盖已有包。包中不包含 Client Secret、密码、数据库或本地启动命令。不同 ChatGPT 界面的导入方式可能变化，以界面实际提供的入口为准。

## 八、管理用户与凭据

访问 `https://自己的域名/admin`，使用安装时创建的管理员账号登录：

- 新建、编辑、停用或恢复业务用户；每个用户分别保存数据与凭据。
- 查看并复制客户端、ChatGPT 配置与同步加密密码。
- 查看当前文件、历史版本和数据库实际占用，不将不同统计口径相加。
- 修改管理员密码或用户名；需要当前密码，保存后撤销所有管理会话。
- 修改业务认证凭据或停用账号会撤销其 OAuth 授权，之后需重新连接。
- 已有数据的同步加密密码只能验证校正，不提供整库换密。

旧账号只有凭据哈希时显示“原文未保存”。可验证补录原凭据，或明确重置；不能从哈希找回原文。管理员和 OAuth 登录页不会由服务端预填用户名；密码管理器自动填充属于浏览器行为。

## 九、升级、备份与回退

### 已有安装升级到 v2.3

在宝塔停止项目，完整备份原数据目录，然后仅替换程序与配套发布文件，再启动原项目。**不要删除主配置，也不要重新执行初始化命令。** 原账号、OAuth 授权、数据库位置、同步密码和写入开关保留。

旧管理注册表 v1 继续读取原固定管理员名；新安装和管理员更名使用注册表 v2。旧二进制不支持 v2，回退必须恢复对应版本的完整管理资料备份，不能仅替换程序。

### 数据备份

新安装的文件布局：

```text
/www/server/spmcp-data/
  config.json
  instance/
    sync.db
    sync-password.txt
    admin/
      master.key
      registry.enc
      users/<用户ID>/
        sync.db
        sync-password.txt
```

旧安装可能仍使用根目录数据库，按原配置保留。备份整个数据目录，包含主配置、管理主密钥、加密注册表、所有用户库和密码文件。主密钥与注册表缺一不可。

停止项目后再备份；运行中仅复制 `.db` 会遗漏 WAL。备份建议保存在网站目录之外的私密位置。

### 安装失败与恢复

首次安装先准备受限暂存文件，完成校验后才发布主配置。重启会清理可确认归属的未完成暂存，或恢复已完整准备但尚未发布的安装。无法确认归属的文件和非空数据目录不会被自动清理。

已安装系统丢失或损坏 `config.json` 时，应从备份恢复；不会自动把原数据当成全新安装。不要通过删除数据目录来解决启动错误。

## 十、常见问题

| 现象 | 处理方式 |
| --- | --- |
| 宝塔项目启动失败 | 核对程序架构、执行权限、`www` 目录权限、端口和项目日志 |
| 网站 502 | 检查项目运行状态、8787 端口和本机 `/healthz` |
| 配置缺失但数据目录非空 | 恢复原配置，或为真正的新安装使用独立空目录 |
| 安装码失效或已使用 | 重启尚未安装的项目，从新日志取码；已有安装直接登录管理面板 |
| 安装要求 HTTPS | 先在宝塔设置 SSL 和 HTTPS 跳转，再通过域名访问 |
| `host forbidden` | 反代传入 `Host $proxy_host`，核对内部端口；不要关闭白名单校验 |
| `Origin does not match` | 浏览器访问地址与配置的 OAuth issuer 必须相同；不要换用 IP、HTTP 或另一个域名登录 |
| OAuth 发现 404 | 检查 `/.well-known/oauth-` 路由没有被宝塔证书验证目录截获 |
| WebDAV 401 | 核对业务用户名与 WebDAV 密码，直接使用 HTTPS |
| 上传后业务 JSON 不可读 | 检查原同步加密密码、文件完整性及 v2/schema v4 兼容范围 |
| MCP 写入未开放 | 查看该账号写入设置与 `get_capabilities`；尚未实现的行为仍会拒绝 |
| 复制按钮失败 | 用 HTTPS，允许浏览器剪贴板，或手动复制／主动下载凭据 |
| Nginx 保存报 location 错误 | location 必须位于该站点 server 内，不能作为独立完整站点配置 |
| 更换公网域名 | 在宝塔更新域名与证书，按现有配置更新所有账号 issuer，再重新连接；不改写同步数据 |

## 源码构建与验证

公开源码只包含指定版本的运行源码、内嵌资源、依赖声明、许可证和发布 CI，不包含开发历史、测试样本、开发工具或运行数据。需要 Go `go.mod` 指定版本。

```sh
go mod download
go vet ./...
CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags="-s -w" -o spmcp ./cmd/spmcp
```

Windows PowerShell 将最后一条改为：

```powershell
$env:CGO_ENABLED='0'
go build -trimpath -buildvcs=false -ldflags="-s -w" -o spmcp.exe ./cmd/spmcp
```

直接构建只需要 Go，不需要 Python 或 Node.js。`web/models.md` 是 MCP 使用的运行时模型参考，已内嵌到二进制，不是开发文档。

需要一次构建三个平台安装包及插件源码包时，使用 Python 3.11 或更新版本执行 `python .github/release.py`，产物位于 `dist/`，并提供 `SHA256SUMS.txt`。

GitHub CI 位于 `.github/workflows/release.yml`：主分支、PR 或手动运行时检查并构建；推送与 `plugin/plugin.json` 匹配的 `v*` 标签时，创建 GitHub Release 并上传三个平台安装包、插件包和校验文件。当前标签可使用 `v2.3` 或 `v2.3.0`。手动运行不创建 Release；不需要个人 PAT。公开源码发布前的行为测试在私有开发仓库完成。

Super Productivity 上游代码为 MIT，保留 Johannes Millan 的版权和固定提交来源。本项目及新增代码使用根目录 `LICENSE` 所述许可；第三方组件遵从各自许可证，具体见 `NOTICE`、`THIRD_PARTY_NOTICES.md` 和 `licenses/`。源码保留必需的编译指令及版权和许可声明。许可证集合也内嵌在二进制中，可使用 `spmcp licenses` 查看，或通过 `spmcp licenses --output /可写路径/licenses.txt` 导出；输出路径必须尚不存在。

宝塔部署说明供自行安装使用；本地测试和交叉构建不代表已在你的服务器完成部署或 ChatGPT 授权。
