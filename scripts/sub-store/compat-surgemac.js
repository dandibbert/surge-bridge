/**
 * Sub-Store -> 组合订阅/订阅 -> 节点操作 -> 脚本操作
 *
 * Keep the Sub-Store -> Surge policy-path flow unchanged.
 * Only change which executable the existing SurgeMac converter uses.
 * This is a COMPATIBILITY adapter for the existing multi-listener merge
 * format; it deliberately does not enable shared-port UDP routing.
 *
 * Set EXEC to the installation path of the compiled Surge Bridge binary.
 */
function operator(proxies, targetPlatform) {
  if (targetPlatform !== "SurgeMac") return proxies;

  const EXEC = "/opt/homebrew/bin/surge-bridge";
  for (const proxy of proxies) {
    if (!proxy || typeof proxy !== "object") continue;

    // A single combined SurgeMac output receives one continuous, automatic
    // port allocation; do not carry per-source starting ports into the merge.
    delete proxy._localPort;

    proxy._exec = EXEC;
    proxy._merge = true;
    proxy._mergeName = "surge-bridge engine";

    // Explicitly route VLESS to the external engine; leave other
    // protocols' existing Surge/Sub-Store decisions alone.
    if (String(proxy.type).toLowerCase() === "vless") {
      proxy._mihomoExternal = true;
    }
  }
  return proxies;
}
