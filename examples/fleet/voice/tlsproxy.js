// TLS forwarder for the phone voice page: https/wss on the tailnet name -> local
// plain ports. A phone browser only hands out its microphone on a secure page,
// and a secure page may only open a secure (wss) socket, so both the page and
// the voice server need a TLS front. Does not touch tailscale serve/funnel.
//
// Usage: node tlsproxy.js 3443:3000 7443:7880
//
// Environment:
//   VOICEMODE_DIR      state directory                  (default ~/.voicemode)
//   VOICE_CERT_DIR     holds m.crt + m.key              (default $VOICEMODE_DIR/certs)
//   VOICE_PUBLIC_HOST  name plain-http callers are sent to
//                      (default: the certificate's own name)
//   VOICE_BIND_HOST    address(es) to listen on, comma separated
//                      (default: this machine's tailnet IPv4 address)
const https = require('https'), http = require('http'), net = require('net'), fs = require('fs');
const os = require('os'), path = require('path'), crypto = require('crypto');
const dir = process.env.VOICE_CERT_DIR ||
  path.join(process.env.VOICEMODE_DIR || path.join(os.homedir(), '.voicemode'), 'certs');
const opts = { cert: fs.readFileSync(path.join(dir, 'm.crt')), key: fs.readFileSync(path.join(dir, 'm.key')) };
function certName() {
  try {
    const m = /(?:^|\n)CN=([^\n]+)/.exec(new crypto.X509Certificate(opts.cert).subject);
    return m ? m[1].trim() : '';
  } catch { return ''; }
}
const publicHost = process.env.VOICE_PUBLIC_HOST || certName() || os.hostname();
// Being in the tailnet is the only access control this path has: the page hands
// a room token, and the turn log, to whoever asks. So the front door listens on
// this machine's tailnet address and nowhere else. A device that merely shares
// the Wi-Fi must not be able to open a connection at all.
function tailnetAddresses() {
  const found = [];
  for (const list of Object.values(os.networkInterfaces())) {
    for (const a of list || []) {
      if (a.internal || !(a.family === 'IPv4' || a.family === 4)) continue;
      const [p, q] = a.address.split('.').map(Number);
      if (p === 100 && q >= 64 && q <= 127) found.push(a.address); // 100.64.0.0/10
    }
  }
  return found;
}
const binds = (process.env.VOICE_BIND_HOST || '').split(',').map(s => s.trim()).filter(Boolean);
if (!binds.length) binds.push(...tailnetAddresses());
if (!binds.length) {
  console.error('tlsproxy: this machine has no tailnet address (is Tailscale running?). Refusing to listen on ' +
    'every network interface instead. Start Tailscale, or set VOICE_BIND_HOST to the address to listen on.');
  process.exit(1);
}
for (const pair of process.argv.slice(2)) {
  const [from, to] = pair.split(':').map(Number);
  const srv = https.createServer(opts, (req, res) => {
    // agent:false = a fresh upstream connection per request, never a pooled one.
    // A pooled connection outlives the server it was opened to: when the page
    // server restarted while another program also listened on its port number,
    // the pool kept sending the phone's requests to that other program.
    const p = http.request({ host: '127.0.0.1', port: to, path: req.url, method: req.method, headers: { ...req.headers, connection: 'close' }, agent: false }, r => {
      res.writeHead(r.statusCode, r.headers); r.pipe(res);
    });
    p.on('error', () => { res.statusCode = 502; res.end('bad gateway'); });
    req.pipe(p);
  });
  srv.on('upgrade', (req, sock, head) => {
    const up = net.connect(to, '127.0.0.1', () => {
      up.write(`${req.method} ${req.url} HTTP/1.1\r\n` +
        Object.entries(req.headers).map(([k, v]) => `${k}: ${v}`).join('\r\n') + '\r\n\r\n');
      if (head && head.length) up.write(head);
      sock.pipe(up); up.pipe(sock);
    });
    up.on('error', () => sock.destroy()); sock.on('error', () => up.destroy());
  });
  // Front door: sniff the first byte. TLS (0x16) goes to the https server on a private
  // port; plain http gets a redirect to https instead of a closed connection.
  const inner = from + 10000;
  srv.listen(inner, '127.0.0.1');
  const door = c => {
    c.once('data', buf => {
      c.pause(); c.unshift(buf);
      if (buf[0] === 0x16) {
        const up = net.connect(inner, '127.0.0.1', () => { c.pipe(up); up.pipe(c); c.resume(); });
        up.on('error', () => c.destroy()); c.on('error', () => up.destroy());
      } else {
        const m = /^Host:\s*([^:\r\n]+)/im.exec(buf.toString('latin1'));
        const host = (m && m[1]) || publicHost;
        const target = host.includes('.') ? host : publicHost;
        c.end(`HTTP/1.1 301 Moved Permanently\r\nLocation: https://${target}:${from}/\r\nContent-Length: 0\r\nConnection: close\r\n\r\n`);
      }
    });
    c.on('error', () => {});
  };
  for (const addr of binds) {
    const front = net.createServer(door);
    front.on('error', e => { console.error(`tlsproxy: cannot listen on ${addr}:${from}: ${e.message}`); process.exit(1); });
    front.listen(from, addr, () => console.log(`${addr}:${from} (https + http redirect) -> :${to}`));
  }
}
