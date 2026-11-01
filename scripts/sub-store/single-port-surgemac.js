/**
 * Surge Bridge single-port Sub-Store adapter.
 *
 * Add to the FINAL COMBINED subscription's 节点操作 -> 脚本操作.
 * Keep Surge directly subscribed to the SAME Sub-Store URL (target=SurgeMac).
 * Requires a Surge Bridge binary supporting bridge-udp-sessions.
 *
 * Original Surge-compatible nodes remain untouched. VLESS (and explicitly
 * _mihomoExternal-tagged nodes) become per-user SOCKS5 entries using ONE port.
 * A single External entry contains the internal bridge configuration.
 *
 * Optional script arguments: namespace, port, exec, udp (true/false).
 * Do not apply another "mihomo merge" transformer after this script.
 */
function operator(proxies, targetPlatform, context) {
  if (String(targetPlatform).toLowerCase() !== "surgemac") return proxies;
  if (!Array.isArray(proxies) || proxies.length === 0) return proxies;

  const opts = (typeof $arguments !== "undefined" && $arguments) || {};
  const md5 = (value) => String(ProxyUtils.hex_md5(String(value))).toLowerCase();
  const source =
    opts.namespace ||
    proxies.find((p) => p && p._collectionName)?._collectionName ||
    proxies.find((p) => p && p._subName)?._subName ||
    "surge-bridge-default";
  // One stable port PER COMBINED OUTPUT, never one port per subscription/node.
  // Derived ports are convenient, not collision-proof across unrelated outputs.
  const autoPort = 20000 + (parseInt(md5(source).slice(0, 8), 16) % 20000);
  const port = opts.port == null || opts.port === "" ? autoPort : Number(opts.port);
  if (!Number.isInteger(port) || port < 1024 || port > 65535) {
    throw new Error("Surge Bridge: port must be an integer from 1024 to 65535");
  }

  const exec = String(opts.exec || "/opt/homebrew/bin/surge-bridge");
  if (!exec.startsWith("/") || /["\r\n]/.test(exec)) {
    throw new Error("Surge Bridge: exec must be an absolute path without quotes or newlines");
  }
  const enabledUDP = !(opts.udp === false || String(opts.udp).toLowerCase() === "false");
  const controllerPort = opts.controllerPort == null || opts.controllerPort === ""
    ? port + 1 : Number(opts.controllerPort);
  if (!Number.isInteger(controllerPort) || controllerPort < 1024 ||
      controllerPort > 65535 || controllerPort === port) {
    throw new Error("Surge Bridge: controllerPort must differ from the SOCKS port");
  }

  const output = [];
  const outbounds = [];
  const users = [];
  const rules = [];
  const knownIDs = new Set();
  const names = new Set(proxies.filter(Boolean).map((p) => p.name));
  const shouldBridge = (p) => p && (
    String(p.type).toLowerCase() === "vless" ||
    String(p.type).toLowerCase() === "ssr" ||
    p._mihomoExternal === true ||
    p._surgeBridge === true
  );

  for (const original of proxies) {
    if (!shouldBridge(original)) {
      output.push(original);
      continue;
    }

    // ProxyUtils.produce() normalizes the internal node using the SAME
    // ClashMeta producer that the built-in SurgeMac Mihomo conversion uses.
    const cloned = JSON.parse(JSON.stringify(original));
    const converted = ProxyUtils.produce(
      [cloned], "ClashMeta", "internal", {"include-unsupported-proxy": true, "delete-underscore-fields": true}
    );
    if (!Array.isArray(converted) || converted.length !== 1 || !converted[0]) {
      throw new Error("Surge Bridge: unable to convert " + original.name);
    }

    const identity = JSON.stringify([
      original._subName || "", original.name, original.type,
      original.server, original.port, original.uuid || "",
      original.password || "", original.username || ""
    ]);
    const id = md5(identity).slice(0, 20);
    // Even duplicate nodes need distinct authentication identities.
    if (knownIDs.has(id)) {
      throw new Error("Surge Bridge: duplicate node identity: " + original.name);
    }
    knownIDs.add(id);

    const username = "sb-" + id;
    // On a loopback listener, this is a stable local auth token. It is NOT
    // intended as a remotely exposed access credential.
    const password = md5("surge-bridge-local:" + JSON.stringify(original));
    // Preserve the human-readable node name inside Yacd/Zashboard too.
    // Strip commas/newlines because Mihomo rule targets are comma-delimited.
    const label = String(original.name || "Unnamed").replace(/[,\r\n]/g, " ").slice(0, 80);
    const outbound = label + " [SB:" + id.slice(0, 8) + "]";
    const internal = converted[0];
    // Guard accidental overrides and keep the original provider name in Surge.
    internal.name = outbound;
    outbounds.push(internal);
    users.push({username, password});
    rules.push("IN-USER," + username + "," + outbound);
    output.push({
      type: "socks5",
      name: original.name,
      server: "127.0.0.1",
      port,
      username,
      password,
      udp: enabledUDP && original.udp !== false,
      ...(original["test-url"] ? {"test-url": original["test-url"]} : {})
    });
  }

  if (outbounds.length === 0) return output;

  // Default to a comment-only carrier: Surge ignores the line and shows
  // only actual proxy nodes, while the standalone Bridge reads the embedded
  // config from the same Sub-Store output. Optional externalBootstrap=true
  // restores a visible Surge External entry for compatibility testing.
  let engineName = opts.externalBootstrap === true
    ? "surge-bridge engine" : "# surge-bridge engine";
  if (names.has(engineName)) engineName += " [" + md5(source).slice(0, 6) + "]";
  const first = proxies.find(shouldBridge) || {};
  const dns = {
    enable: true,
    ipv6: true,
    "default-nameserver": first._defaultNameserver || [
      "180.76.76.76", "52.80.52.52", "119.28.28.28", "223.6.6.6"
    ],
    nameserver: first._nameserver || [
      "https://doh.pub/dns-query",
      "https://dns.alidns.com/dns-query",
      "https://doh-pure.onedns.net/dns-query"
    ]
  };
  // The API is always loopback-only and requires a secret, including for
  // dashboards. The optional script argument "secret" overrides the stable
  // derived local secret. Never expose this API on a LAN address.
  const apiSecret = String(opts.secret ||
    md5("surge-bridge-api:" + JSON.stringify(
      proxies.filter(shouldBridge).map(p => [p.uuid || "", p.password || "", p.server])
    )));
  const cfg = {
    "external-controller": "127.0.0.1:" + controllerPort,
    secret: apiSecret,
    mode: "rule",
    ipv6: true,
    "log-level": "info",
    dns,
    proxies: outbounds,
    listeners: [{
      name: "surge-bridge-socks",
      type: "socks",
      listen: "127.0.0.1",
      port,
      udp: enabledUDP,
      "bridge-udp-sessions": enabledUDP,
      users
    }],
    rules: [...rules, "MATCH,REJECT"]
  };

  output.push({
    type: "external",
    name: engineName,
    exec,
    "local-port": port,
    args: ["-config", ProxyUtils.Base64.encode(JSON.stringify(cfg))],
    udp: enabledUDP,
    addresses: []
  });
  return output;
}
