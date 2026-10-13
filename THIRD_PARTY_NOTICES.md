# 第三方组件与许可说明

本项目主许可证为 MIT。以下组件保留各自许可，不因项目根目录 LICENSE 而重新许可为 MIT。

Super Productivity 的移植业务、模型及生成样本来自固定提交 `71eb7780bcf5d6b1dfdcd39a8a8265547d040760`；MIT 版权为 Johannes Millan。见 LICENSE、NOTICE。

## 生产 Go 模块

完整原文和作者文件位于 `licenses/modules/`；Go 标准库主许可位于 `licenses/go/`。不同平台实际模块集合见 `licenses/manifest.json`。

| 模块 | 版本 | 主许可 | 构建目标 |
| --- | --- | --- | --- |
| `github.com/dustin/go-humanize` | `v1.0.1` | MIT | windows-amd64, linux-amd64, linux-arm64 |
| `github.com/google/jsonschema-go` | `v0.4.3` | MIT | windows-amd64, linux-amd64, linux-arm64 |
| `github.com/google/uuid` | `v1.6.0` | BSD-3-Clause | windows-amd64, linux-amd64, linux-arm64 |
| `github.com/mattn/go-isatty` | `v0.0.24` | MIT | windows-amd64 |
| `github.com/modelcontextprotocol/go-sdk` | `v1.7.0` | Apache-2.0 / MIT（不同贡献并存） | windows-amd64, linux-amd64, linux-arm64 |
| `github.com/ncruces/go-strftime` | `v1.0.0` | MIT | windows-amd64 |
| `github.com/remyoudompheng/bigfft` | `v0.0.0-20230129092748-24d4a6f8daec` | BSD-3-Clause | windows-amd64, linux-amd64, linux-arm64 |
| `github.com/segmentio/asm` | `v1.1.3` | MIT | windows-amd64, linux-amd64, linux-arm64 |
| `github.com/segmentio/encoding` | `v0.5.4` | MIT | windows-amd64, linux-amd64, linux-arm64 |
| `github.com/yosida95/uritemplate/v3` | `v3.0.2` | BSD-3-Clause | windows-amd64, linux-amd64, linux-arm64 |
| `golang.org/x/crypto` | `v0.57.0` | BSD-3-Clause | windows-amd64, linux-amd64, linux-arm64 |
| `golang.org/x/oauth2` | `v0.35.0` | BSD-3-Clause | windows-amd64, linux-amd64, linux-arm64 |
| `golang.org/x/sync` | `v0.23.0` | BSD-3-Clause | windows-amd64, linux-amd64, linux-arm64 |
| `golang.org/x/sys` | `v0.48.0` | BSD-3-Clause | windows-amd64, linux-amd64, linux-arm64 |
| `golang.org/x/time` | `v0.15.0` | BSD-3-Clause | windows-amd64, linux-amd64, linux-arm64 |
| `modernc.org/libc` | `v1.77.1` | BSD-3-Clause | windows-amd64, linux-amd64, linux-arm64 |
| `modernc.org/mathutil` | `v1.7.1` | BSD-3-Clause | windows-amd64, linux-amd64, linux-arm64 |
| `modernc.org/memory` | `v1.12.1` | BSD-3-Clause | windows-amd64, linux-amd64, linux-arm64 |
| `modernc.org/sqlite` | `v1.60.1` | BSD-3-Clause | windows-amd64, linux-amd64, linux-arm64 |

## 文件级与继承许可

- SDK v1.7.0 为 Apache-2.0 和原 MIT 贡献并存；非规范文档另为 CC-BY-4.0。未随本包复制 SDK 文档，不能把该声明解释为整项目 CC-BY。
- modernc SQLite Go 驱动是 BSD；SQLite C 部分为公共领域。sqlite-vec 扩展未被当前生产构建导入。libc 另含 musl、go-netdb、Nixpkgs、uint128、平台头文件等来源。
- 保留依赖提供的 LICENSE-3RD-PARTY，以及构建文件中的完整注释组，见 `licenses/compiled-source-notices.md`。其中包括 Luke Champine、Lucent/Vita Nuova、Sun Microsystems、Cephes、fiat-crypto、mingw-w64、Lockless 和平台来源声明。
- 生成头文件保留 LGPL-2.1-or-later、GPL-3.0-or-later + GCC Runtime Library Exception 声明。libc 的相关文件是类型/常量/布局，没有 Go 函数定义；LGPL 2.1 第 5 节与 GCC 运行库例外需按实际材料判断。附 GNU 原始许可和例外用于来源记录，不能据关键词认定整个应用使用 GPL，也不宣称完成了全部生成源码的逐行法律审查。
- modernc 自带汇总还包括未链接的测试、模块图与图像材料；提供原始文件保留上下文，不表示本程序使用全部内容。MPL 的 golang-lru 不在三平台构建模块中，未启用 boringcrypto，也不复制依赖 logo。

## 独立插件

插件源码包只包含项目 manifest、Skill 和项目 LICENSE/NOTICE，不含 Go 服务或 npm 运行库。各独立交付物按其实际内容附许可资料。

本清单对应 go.mod 精确版本及三平台实际构建文件。补齐交付资料不代表已完成所有第三方材料的法律定性。
