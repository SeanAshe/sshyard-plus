# 正式发布签名配置

在 GitHub 仓库 Settings → Secrets and variables → Actions → Repository secrets 中配置以下六项；证书和 Apple API 私钥只存入 Secrets，不提交到仓库

| Secret | 内容 |
| --- | --- |
| `MYGO_UPDATER_PRIVATE_KEY` | `/Users/licoy/Documents/mac-cert/sshyard/mygo-update.key` 的完整内容，与 `mygo.json` 的 `updates.publicKey` 配对；同目录 `.pub` 是公钥 |
| `MACOS_CERTIFICATE_P12_BASE64` | Developer ID Application 证书及其私钥导出的 `.p12` 文件，整体 Base64 编码 |
| `MACOS_CERTIFICATE_PASSWORD` | 导出 `.p12` 时设置的密码，这是证书导出密码，与 Mac 登录密码无关 |
| `APPLE_API_KEY_P8` | `/Users/licoy/Documents/mac-cert/AuthKey_2N46T35DQ8.p8` 文件整体 Base64 编码，包含 BEGIN/END 行与换行；Secret 名称保持不变 |
| `APPLE_API_KEY_ID` | `2N46T35DQ8` |
| `APPLE_API_ISSUER_ID` | 对应 App Store Connect 团队 API 密钥的 Issuer ID，UUID 格式，不是 Team ID `395QP35ZGC` |

在本机执行以下命令可将证书 Base64 直接复制到剪贴板，然后粘贴到 `MACOS_CERTIFICATE_P12_BASE64`

```sh
base64 -i /Users/licoy/Documents/mac-cert/mac-sing.p12 | pbcopy
```

将 Apple API 私钥文件整体 Base64 编码并复制到剪贴板，然后粘贴到 `APPLE_API_KEY_P8`

```sh
base64 -i /Users/licoy/Documents/mac-cert/AuthKey_2N46T35DQ8.p8 | pbcopy
```

Issuer ID 从 [App Store Connect](https://appstoreconnect.apple.com/access/integrations/api) → Users and Access → Integrations → App Store Connect API → Team Keys 页面获取；这里使用团队 API 密钥，该密钥必须有提交公证的权限；个人 API 密钥不适用于此工作流

## 工作流行为

1. `release.yml` 先用 `.github/fork-version.sh` 算出 `<mygo.json 版本>_<相对 Licoy/sshyard main 超前的提交数>`，标签必须是 `v` 加这个结果；`mygo.json` 保持上游的 `x.y.z`，不提交拼接后的版本。只构建 Windows x64 与 Arm64，`MYGO_UPDATER_PRIVATE_KEY` 必须非空，已有正式发布会拒绝覆盖；同时按 `cliff.toml` 用 git-cliff 生成上一个正式标签以来的发布说明，并等待同一标签上的 `ci.yml` 通过；标签指向的提交已在 main 上通过完整 CI 时（仍在运行则等它结束），标签上的 `ci.yml` 直接复用该结果，不再重复测试和构建
2. `go tool mygo build` 签署 Windows 更新清单；构建时把发布说明写入更新清单，供应用内更新窗口显示，并验证清单有签名且带说明
3. Windows x64 与 Arm64 都构建成功且 CI 通过后，汇总安装包、绿色版 ZIP、更新归档、delta 与更新清单，生成 `SHA256SUMS`，在发布说明后追加 Windows 下载链接，先建草稿上传全部资产，再自动发布为 latest

Windows Arm64 在 GitHub 的 `windows-11-arm` 上原生构建，与 x64 一起进入 CI 产物和正式 Release

Windows 的普通 CI、beta 与正式 Release 都额外按架构提供 `sshyard-<版本>-windows-<amd64|arm64>-portable.zip`；ZIP 直接归档 MyGo 构建的程序及完整资源，排除安装器、安装脚本和更新产物；正式 Release 将其纳入 `SHA256SUMS` 并提供下载链接，解压到可写目录后运行 `SSHYard.exe` 即可，无需安装；应用数据仍保存在 `%APPDATA%\SSHYard`，已保存凭据仍绑定当前 Windows 用户，不代表数据可随 ZIP 跨设备迁移

本地可运行 `python3 .github/windows-portable-test.py` 验证 ZIP 内容、无签名 beta 产物及下载链接，Windows CI 在归档前也会执行这些检查

任一平台构建或 CI 失败时不会创建 Release；修复或重跑 CI 的失败 job 后，重跑本工作流的失败 job 即可；之前运行留下的草稿会被替换，已发布的版本不会被覆盖

发布时不要改 `mygo.json` 的版本。在要发布的提交上执行 `git tag -a "v$(bash .github/fork-version.sh)" -m "Release"` 并推送该标签；打包时工作流把上游版本和超前提交数写进运行器上的 `mygo.json`，仓库里的版本文件保持上游的 `x.y.z`。`v<version>-beta-<yyyymmddhhmmss>` 继续只运行 `ci.yml`，不会触发正式签名发布

允许工作区存在其他已暂存、未暂存或未跟踪改动，版本提交只包含 `mygo.json`，其他文件及其暂存状态保持不变；`mygo.json` 本身已有的其他编辑会随该文件一并提交

本地可运行 `python3 .github/bump-test.py`，在临时 Git 仓库中验证版本提交、混合改动保留和推送流程，推送目标仅为测试创建的本地裸仓库

无凭据的失败路径检查可本地运行 `bash .github/macos-release-test.sh`，覆盖缺少 Secrets、签名身份解析与零个或多个身份的拒绝、钥匙串删除失败仍清理文件并保留失败状态

本地静态检查无法代替真实 Apple 签名、公证和 Gatekeeper 验收，首次完整验收需要配置上述 Secrets 后运行正式发布工作流
