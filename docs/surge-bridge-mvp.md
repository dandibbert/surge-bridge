# Surge Bridge — MVP（开发中）

基于 Mihomo `Meta` 分支的 Surge for macOS 专用内核。**Surge 继续直接订阅 Sub-Store；本项目不介入订阅网络请求，不管理 Surge 的主配置。**

## 当前已实现

- 与上游 Mihomo 相同的 `-config <base64>`、`-f`、`-t` 启动方式；可作为 External Proxy 的程序入口。
- 内核内部独立的单节点诊断 API，保留 URLTest 错误（与 Yacd 所使用的旧 `/proxies/{name}/delay` 路由互不影响）。
- 诊断结果包括节点名、出站类型、请求耗时、延迟值、失败类别、失败文本；最近 100 条记录留在内存中；最多 8 个并发测试。
- 使用 Sub-Store 节点脚本在**现有聚合模式**中替换程序路径；不额外生成 Surge 主配置。
- macOS ARM64 构建工作流（见 GitHub Actions）。

## 当前未实现 / 不应被误认为已经解决

- **单端口 UDP 会话隔离已实现首轮代码，但尚未完成 Surge 实机验证。** 通过可选的 `bridge-udp-sessions: true`，已认证 TCP 控制会话会获得单独的临时 UDP 中继端口；关闭控制会话会释放端口。不会开放绕过认证的共用 UDP Listener。**Sub-Store 适配脚本仍保留旧多 Listener 模式**，暂不作为生产默认行为，也不能宣称已经彻底解决 localPort。
- 单进程在 Surge 未使用 External 策略时的可靠常驻、`launchd` 自恢复与订阅热更新尚未实现。
- 错误分类为**基于错误类型/文本的最佳努力推测**，不是对 DNS/TCP/TLS/REALITY 各阶段做过完整埋点。不能把猜测显示成已确认成功/失败的握手阶段。
- 还没有 SwiftUI 菜单栏 UI，也没有与 Surge 的外部策略单测 API 集成。
- 尚未完成真实 Surge、VLESS、UDP 流量联调；不要直接替换生产环境的代理内核。

## 用法（当前聚合模式）

1. 从 GitHub Actions 下载 `surge-bridge-darwin-arm64`（或使用 Go 编译），放在 `/opt/homebrew/bin/surge-bridge` 并赋予执行权限。
2. 在 Sub-Store **组合订阅 → 节点操作 → 脚本操作** 添加 [compat-surgemac.js](../scripts/sub-store/compat-surgemac.js)，放在需要应用的节点筛选操作之后。
3. 保持原有 `target=SurgeMac`、`policy-path` 及其他 Surge 规则不变。适配脚本会清除节点级 `_localPort`，在**单个组合订阅输出**中由 Sub-Store 自动连续分配端口，无需为每个来源记住起始端口。**多个独立 SurgeMac 输出仍可能端口冲突**；现阶段不要启用“所有节点共用一个 SOCKS5 端口”的实验脚本。
4. 先用少量节点测试，确认聚合 External 项启动，原节点仍可用，再增加节点数量。

### 单独测试节点并查看原始错误

如果 Mihomo 配置已经启用了 `external-controller: 127.0.0.1:9090` 及 `secret`（例如你的 Yacd 已在连接同一个控制器），可以调用新增 API。用 API 中的节点名，**不是 Surge 策略组名**。当前 Sub-Store 聚合模式仍可能把内部出站命名为端口号：

```bash
API=http://127.0.0.1:9090
read -rs "TOKEN?Mihomo API Secret: "; echo
curl -fsSG "$API/surge-bridge/proxies/56983/test" \
  -H "Authorization: Bearer $TOKEN" \
  --data-urlencode "timeout=8000" \
  --data-urlencode "url=https://www.gstatic.com/generate_204"
```

诊断历史：

```bash
curl -fsS -H "Authorization: Bearer $TOKEN" "$API/surge-bridge/diagnostics"
```

运行状态：

```bash
curl -fsS -H "Authorization: Bearer $TOKEN" "$API/surge-bridge/status"
```

所有新增路由都挂在**原 Mihomo External Controller 的鉴权中间件之下**，不额外启动一个无鉴权端口。请让控制器只监听回环地址，设置强随机 `secret`。

### TCP 单端口技术试验（独立于生产 Sub-Store）

参见 [single-port-tcp-only.yaml](../examples/single-port-tcp-only.yaml)。此示例现在启用**实验性的会话隔离 UDP**，由同一个 SOCKS5 TCP 端口按用户名分流，UDP ASSOCIATE 每会话分配一个独立的临时 UDP 端口。须验证 Surge 客户端是否遵循服务器返回的 BND.ADDR/BND.PORT，以及 UDP 包的生命周期、并发与来源端口兼容性，尚不可投入生产。

## 开发约束

- **不修改原有 Surge → Sub-Store 订阅 URL 链路**。
- 在能保证 UDP 不串线之前，不发布单端口多用户的生产配置。
- 优先维持与 Mihomo 上游兼容；不要在 Source 中记录真实订阅、UUID、密码或 API Secret。
