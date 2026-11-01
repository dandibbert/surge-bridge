// Real local loopback integration test for the compiled Surge Bridge binary.
// No public proxy, paid service, DNS, or external Internet access required.
const test = require('node:test');
const assert = require('node:assert/strict');
const net = require('node:net');
const dgram = require('node:dgram');
const {spawn} = require('node:child_process');
const {once} = require('node:events');

async function freePort() {
  const s = net.createServer();
  s.listen(0, '127.0.0.1');
  await once(s, 'listening');
  const port = s.address().port;
  await new Promise(resolve => s.close(resolve));
  return port;
}
function read(socket, timeout = 5000) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => { cleanup(); reject(new Error('TCP reply timed out')); }, timeout);
    const data = d => { cleanup(); resolve(d); };
    const error = e => { cleanup(); reject(e); };
    const closed = () => { cleanup(); reject(new Error('socket closed before reply')); };
    function cleanup() {
      clearTimeout(timer);
      socket.off('data', data); socket.off('error', error); socket.off('close', closed);
    }
    socket.once('data', data);
    socket.once('error', error);
    socket.once('close', closed);
  });
}
async function handshake(port, user, pass, cmd, dstPort = 0) {
  const sock = net.connect(port, '127.0.0.1');
  await once(sock, 'connect');
  let reply = read(sock);
  sock.write(Buffer.from([5,1,2]));
  assert.equal((await reply)[1], 2, 'username/password is required');
  const u = Buffer.from(user), p = Buffer.from(pass);
  reply = read(sock);
  sock.write(Buffer.concat([Buffer.from([1,u.length]),u,Buffer.from([p.length]),p]));
  assert.equal((await reply)[1], 0, 'SOCKS5 authentication succeeded');
  reply = read(sock);
  sock.write(Buffer.from([5,cmd,0,1,127,0,0,1,dstPort >> 8,dstPort & 255]));
  const address = await reply;
  assert.equal(address[1], 0, 'SOCKS5 CONNECT/ASSOCIATE succeeded');
  assert.equal(address[3], 1, 'expected IPv4 SOCKS5 BND address');
  return {sock, relayPort: address.readUInt16BE(8)};
}
async function udpWait(socket, timeout = 3000) {
  return Promise.race([
    once(socket,'message').then(([buf]) => buf),
    new Promise((_, reject) => setTimeout(() => reject(new Error('UDP reply timeout')), timeout))
  ]);
}
async function sendUDP(socket, relayPort, targetPort, payload) {
  const header = Buffer.from([0,0,0,1,127,0,0,1,targetPort>>8,targetPort&255]);
  await new Promise((resolve, reject) =>
    socket.send(Buffer.concat([header,Buffer.from(payload)]), relayPort, '127.0.0.1', err => err ? reject(err) : resolve()));
}
async function udpSocket() {
  const s = dgram.createSocket('udp4');
  s.bind(0, '127.0.0.1'); await once(s,'listening'); return s;
}
async function waitListen(port, child) {
  for(let i = 0; i < 100; i++) {
    if(child.exitCode !== null) throw Error('Bridge exited before listener started');
    const socket = net.connect(port,'127.0.0.1');
    try { await Promise.race([once(socket,'connect'), new Promise((_,rej)=>setTimeout(()=>rej(Error('wait')),100))]); socket.destroy(); return; }
    catch { socket.destroy(); await new Promise(r => setTimeout(r,100)); }
  }
  throw Error('Bridge did not open its TCP listener');
}

test('single-port users keep TCP and UDP routes isolated', {timeout: 60000}, async () => {
  const binary = process.env.BRIDGE_TEST_BINARY || './dist/surge-bridge-linux-amd64';
  const port = await freePort();
  const httpServer = net.createServer(socket => socket.on('data', data => {
    if (data.toString().includes('GET /')) socket.end('HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nOK');
  }));
  httpServer.listen(0,'127.0.0.1'); await once(httpServer,'listening');
  const httpPort = httpServer.address().port;
  const echo = await udpSocket();
  let received = 0;
  echo.on('message', (msg,peer) => { received++; echo.send(msg, peer.port, peer.address); });
  const echoPort = echo.address().port;
  const config = {
    mode:'rule',
    ipv6:false,
    dns:{enable:false},
    listeners:[{
      name:'bridge-socks',type:'socks',listen:'127.0.0.1',port,udp:true,
      'bridge-udp-sessions':true,
      users:[{username:'alice',password:'a-pass'},{username:'bob',password:'b-pass'}]
    }],
    rules:['IN-USER,alice,DIRECT','IN-USER,bob,REJECT','MATCH,REJECT']
  };
  const proc = spawn(binary, ['-config',Buffer.from(JSON.stringify(config)).toString('base64')], {stdio:['ignore','pipe','pipe']});
  let logs = '';
  proc.stderr.on('data', d => { logs += d.toString(); });
  proc.stdout.on('data', d => { logs += d.toString(); });
  const sockets = [];
  try {
    await waitListen(port, proc);
    const alice = await handshake(port,'alice','a-pass',1,httpPort);
    sockets.push(alice.sock);
    const response = read(alice.sock);
    alice.sock.write('GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n');
    assert.match((await response).toString(), /200 OK/);

    const ua = await handshake(port,'alice','a-pass',3);
    const ub = await handshake(port,'bob','b-pass',3);
    sockets.push(ua.sock,ub.sock);
    assert.notEqual(ua.relayPort,ub.relayPort,'every authenticated control connection gets its own UDP port');
    const ca = await udpSocket();
    const cb = await udpSocket();
    try {
      const aReply = udpWait(ca,4000);
      await sendUDP(ca,ua.relayPort,echoPort,'hello-alice');
      assert.ok((await aReply).toString().includes('hello-alice'), 'Alice receives direct UDP response');
      const before = received;
      await sendUDP(cb,ub.relayPort,echoPort,'should-not-be-direct');
      await new Promise(r=>setTimeout(r,500));
      assert.equal(received,before, 'Bob REJECT UDP must not reach the echo server');
      // A second UDP source must not hijack Alice's pinned association.
      await sendUDP(cb,ua.relayPort,echoPort,'hijack');
      await new Promise(r=>setTimeout(r,300));
      assert.equal(received,before, 'other UDP source port cannot reuse authenticated association');
    } finally { ca.close(); cb.close(); }
  } catch (e) {
    e.message += '\nBridge output:\n' + logs.slice(-5000);
    throw e;
  } finally {
    sockets.forEach(s => s.destroy());
    echo.close();
    await new Promise(resolve => httpServer.close(resolve));
    proc.kill('SIGTERM');
  }
});
