// Real-core watcher test: the same Sub-Store SurgeMac response is read by
// Bridge without proxying or changing any Surge subscription URL.
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const http = require('node:http');
const net = require('node:net');
const {spawn} = require('node:child_process');
const {once} = require('node:events');

async function freePort() {
  const s = net.createServer().listen(0,'127.0.0.1');
  await once(s,'listening'); const port=s.address().port;
  await new Promise(r=>s.close(r)); return port;
}
async function waitPort(port, child) {
  for (let i=0;i<100;i++) {
    if (child.exitCode !== null) throw Error('engine exited early');
    try {
      const c = net.connect(port,'127.0.0.1');
      await once(c,'connect'); c.destroy(); return;
    } catch { await new Promise(r=>setTimeout(r,100)); }
  }
  throw Error('engine never listened');
}
function recv(socket,timeout=3000) {
  return new Promise((resolve,reject)=>{
    const t=setTimeout(()=>{clean();reject(Error('read timeout'));},timeout);
    const data=d=>{clean();resolve(d);}, error=e=>{clean();reject(e);};
    function clean(){clearTimeout(t);socket.off('data',data);socket.off('error',error);}
    socket.once('data',data);socket.once('error',error);
  });
}
async function requestThroughSocks(proxyPort, targetPort) {
  const s=net.connect(proxyPort,'127.0.0.1');
  await once(s,'connect');
  try {
    let r=recv(s);s.write(Buffer.from([5,1,2]));assert.equal((await r)[1],2);
    r=recv(s);s.write(Buffer.from([1,5,...Buffer.from('alice'),6,...Buffer.from('secret')]));assert.equal((await r)[1],0);
    r=recv(s);s.write(Buffer.from([5,1,0,1,127,0,0,1,targetPort>>8,targetPort&255]));assert.equal((await r)[1],0);
    r=recv(s,1000);
    s.write('GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n');
    return await r.catch(()=>Buffer.alloc(0));
  } finally {s.destroy();}
}
async function stop(child) {
  if (!child || child.exitCode !== null) return;
  child.kill('SIGTERM');
  await Promise.race([once(child,'exit'),new Promise(r=>setTimeout(r,3000))]);
  if (child.exitCode === null) child.kill('SIGKILL');
}
async function waitFileJSON(file, want) {
  for(let i=0;i<100;i++){
    try {if(fs.readFileSync(file,'utf8')===want)return;}catch {}
    await new Promise(r=>setTimeout(r,100));
  }
  throw Error('cache did not update with new validated subscription');
}
test('standalone engine hot reloads its read-only Sub-Store source and falls back to cache', {timeout: 70000}, async () => {
  const binary=process.env.BRIDGE_TEST_BINARY || './dist/surge-bridge-linux-amd64';
  const tmp=fs.mkdtempSync(path.join(os.tmpdir(),'bridge-substore-'));
  const cache=path.join(tmp,'last-good.json');
  const proxyPort=await freePort(), apiPort=await freePort();
  let originRequests=0;
  const origin=http.createServer((req,res)=>{originRequests++;res.writeHead(200);res.end('ok');});
  origin.listen(0,'127.0.0.1'); await once(origin,'listening');
  const targetPort=origin.address().port;
  const makeConfig=target=>JSON.stringify({
    mode:'rule',ipv6:false,dns:{enable:false},
    'external-controller':'127.0.0.1:'+apiPort,secret:'test-secret',
    listeners:[{name:'standalone',type:'socks',listen:'127.0.0.1',port:proxyPort,
      udp:false,users:[{username:'alice',password:'secret'}]}],
    rules:['IN-USER,alice,'+target,'MATCH,REJECT']
  });
  let current=makeConfig('DIRECT'), available=true;
  const source=http.createServer((req,res)=>{
    if(!available){res.writeHead(503);res.end('down');return;}
    if(req.url.indexOf('target=SurgeMac')<0){res.writeHead(400);res.end('wrong format');return;}
    const b64=Buffer.from(current).toString('base64');
    res.writeHead(200,{'content-type':'text/plain'});
    res.end('# surge-bridge engine=external,exec="/opt/homebrew/bin/surge-bridge",local-port='+
      proxyPort+',args="-config",args="'+b64+'"\n');
  });
  source.listen(0,'127.0.0.1'); await once(source,'listening');
  const sourceURL='http://127.0.0.1:'+source.address().port+'/download/combined';
  const spawnEngine=()=>{
    const p=spawn(binary,['-bridge-source',sourceURL,'-bridge-cache',cache,'-bridge-poll','1m'],{stdio:['ignore','pipe','pipe']});
    p.logs='';
    p.stderr.on('data',d=>p.logs+=d.toString());p.stdout.on('data',d=>p.logs+=d.toString());
    return p;
  };
  let engine;
  try {
    engine=spawnEngine();await waitPort(proxyPort,engine);
    await waitFileJSON(cache,current);
    const response=await requestThroughSocks(proxyPort,targetPort);
    assert.match(response.toString(),/200 OK/);
    assert.equal(originRequests,1);

    current=makeConfig('REJECT');
    engine.kill('SIGHUP');
    await waitFileJSON(cache,current);
    await requestThroughSocks(proxyPort,targetPort);
    await new Promise(r=>setTimeout(r,150));
    assert.equal(originRequests,1,'reloaded REJECT should not reach origin');

    available=false;await stop(engine);
    engine=spawnEngine();await waitPort(proxyPort,engine);
    await requestThroughSocks(proxyPort,targetPort);
    await new Promise(r=>setTimeout(r,150));
    assert.equal(originRequests,1,'offline restart must preserve cached REJECT rules');
  } catch(error) {
    error.message+='\nEngine logs:\n'+(engine?.logs || '').slice(-6000);
    throw error;
  } finally {
    await stop(engine);
    await new Promise(r=>source.close(r));
    await new Promise(r=>origin.close(r));
    fs.rmSync(tmp,{recursive:true,force:true});
  }
});
