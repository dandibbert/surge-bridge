// Dependency-free contract tests for the Sub-Store node-operation adapter.
// Run: node --test scripts/sub-store/single-port-surgemac.test.cjs
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const crypto = require('node:crypto');

const script = fs.readFileSync(__dirname + '/single-port-surgemac.js', 'utf8');
const sample = () => [
  {_collectionName: 'my-combo', _subName: 'a', name: 'Hong Kong', type: 'vless',
    server: 'hk.example.invalid', port: 443, uuid: 'a-known-uuid', udp: true},
  {_collectionName: 'my-combo', _subName: 'b', name: 'Native SS', type: 'ss',
    server: 'native.example.invalid', port: 1234, password: 'native-secret'},
  {_collectionName: 'my-combo', _subName: 'b', name: 'Tokyo', type: 'vless',
    server: 'jp.example.invalid', port: 443, uuid: 'another-known-uuid'}
];
function apply(proxies, platform = 'SurgeMac', args = {}) {
  const utils = {
    hex_md5: input => crypto.createHash('md5').update(input).digest('hex'),
    Base64: {encode: input => Buffer.from(input).toString('base64')},
    produce: (entries, dest, type) => {
      assert.equal(dest, 'ClashMeta');
      assert.equal(type, 'internal');
      return entries.map(entry => ({...entry}));
    }
  };
  const sandbox = {ProxyUtils: utils, $arguments: args};
  vm.createContext(sandbox);
  vm.runInContext(script, sandbox, {filename: 'single-port-surgemac.js'});
  return sandbox.operator(proxies, platform, {});
}
function parseExternal(out) {
  const ext = out.find(p => p.type === 'external');
  assert.ok(ext, 'one bootstrap External entry is required');
  assert.equal(ext.args[0], '-config');
  return JSON.parse(Buffer.from(ext.args[1], 'base64').toString());
}
test('one SOCKS port and one listener, native nodes untouched', () => {
  const input = sample();
  const before = JSON.stringify(input);
  const out = apply(input);
  assert.equal(JSON.stringify(input), before, 'operator must not mutate source nodes');
  assert.equal(out.length, 4);
  const socks = out.filter(p => p.type === 'socks5');
  assert.equal(socks.length, 2);
  assert.deepEqual(socks.map(p => p.name), ['Hong Kong','Tokyo']);
  assert.equal(new Set(socks.map(p => p.port)).size, 1);
  assert.equal(out[1], input[1]);
  const config = parseExternal(out);
  assert.equal(config.mode, 'rule');
  assert.equal(config.proxies.length, 2);
  assert.equal(config.listeners.length, 1);
  assert.equal(config.listeners[0].port, socks[0].port);
  assert.equal(config.listeners[0]['bridge-udp-sessions'], true);
  assert.equal(config.listeners[0].users.length, 2);
  assert.deepEqual(config.rules.length, 3);
  for (const [i, p] of socks.entries()) {
    assert.equal(config.listeners[0].users[i].username, p.username);
    assert.equal(config.listeners[0].users[i].password, p.password);
    assert.equal(config.rules[i], 'IN-USER,' + p.username + ',' + config.proxies[i].name);
  }
  assert.equal(config.rules.at(-1), 'MATCH,REJECT');
});
test('stable mapping across reordering, no per-node localPort', () => {
  const forward = apply(sample());
  const back = apply(sample().reverse());
  const byName = a => Object.fromEntries(a.filter(p => p.type === 'socks5').map(p => [p.name, {port:p.port,user:p.username,password:p.password}]));
  assert.deepEqual(byName(forward), byName(back));
  assert.ok(forward.find(p => p.type === 'external'));
});
test('explicit TCP-only mode closes user UDP listener', () => {
  const out = apply(sample(), 'SurgeMac', {udp: false, port: 17999});
  assert.equal(out.filter(p => p.type === 'socks5').every(p => p.udp === false), true);
  const config = parseExternal(out);
  assert.equal(config.listeners[0].udp, false);
  assert.equal(config.listeners[0]['bridge-udp-sessions'], false);
  assert.equal(config.listeners[0].port, 17999);
});
test('skip other platforms and untouched native-only subscriptions', () => {
  const input = sample();
  assert.equal(apply(input, 'Surge'), input);
  const native = [input[1]];
  assert.equal(apply(native).length, 1);
});
test('invalid port and duplicate identity fail closed', () => {
  assert.throws(() => apply(sample(),'SurgeMac',{port: 99999}),/port/);
  assert.throws(() => apply([sample()[0], sample()[0]], 'SurgeMac'), /duplicate node identity/);
});
