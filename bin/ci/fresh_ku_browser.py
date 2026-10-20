"""Host-loopback browser harness for the disposable Plan #64 proof only.

Never imported by product code. Credentials remain in memory; only Ingress is
relayed. Grant/restart handlers operate on this task's original journal only.
"""
import base64
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import subprocess
import threading


def exercise(command, call, grant, restart, probe, admin, old_id, new_id, out):
    selected = [None]

    class Relay(BaseHTTPRequestHandler):
        def log_message(self, *args): pass
        def do_GET(self): self.forward()
        def do_POST(self): self.forward()
        def do_PUT(self): self.forward()
        def forward(self):
            body = self.rfile.read(int(self.headers.get("Content-Length", 0)))
            try:
                if self.path.startswith('/__ku/'):
                    if self.path == '/__ku/fixture' and self.command == 'GET':
                        data = dict(admin=admin, target=old_id, keeper=new_id)
                    else:
                        assert self.command == 'POST' and body == b'{}'
                        code, state = call('api-keys/operations/latest')
                        assert code == 200
                        op = state['operation']
                        assert op['targetId'] == old_id and op['keeperId'] == new_id and op['mode'] == 'replace'
                        if selected[0] is None: selected[0] = op['id']
                        assert selected[0] == op['id']
                        if self.path == '/__ku/grant':
                            assert state['nextStep'] in ('add', 'revoke')
                            grant(op['id'], state['nextStep'])
                        elif self.path == '/__ku/restart':
                            assert state['nextStep'] == 'revoke'
                            restart(op['id'])
                        else: raise ValueError('unknown test action')
                        data = dict(ok=True)
                    value = dict(status=200, headers=[('Content-Type','application/json'),('Cache-Control','no-store')], body=base64.b64encode(json.dumps(data).encode()).decode())
                else:
                    program = '''import base64,http.client,json,sys
v=json.load(sys.stdin);c=http.client.HTTPConnection('127.0.0.1',28471,timeout=15)
c.request(v['method'],v['path'],base64.b64decode(v['body']),v['headers'])
r=c.getresponse();print(json.dumps({'status':r.status,'headers':r.getheaders(),'body':base64.b64encode(r.read()).decode()}))'''
                    value = json.loads(command('docker','exec','-i',probe,'python3','-c',program,data=json.dumps(dict(method=self.command,path=self.path,headers={k:v for k,v in self.headers.items() if k.lower() not in ('host','connection','content-length')},body=base64.b64encode(body).decode()))))
                data = base64.b64decode(value['body'])
                self.send_response(value['status'])
                for k,v in value['headers']:
                    if k.lower() not in ('connection','transfer-encoding','content-length'): self.send_header(k,v)
                self.send_header('Content-Length',str(len(data)));self.end_headers();self.wfile.write(data)
            except Exception:
                self.send_response(502);self.end_headers()

    relay = ThreadingHTTPServer(('127.0.0.1',0),Relay)
    threading.Thread(target=relay.serve_forever,daemon=True).start()
    try:
        # Neither credential nor fixture secret is passed on the command line.
        p = subprocess.run(['node',str(Path(__file__).with_name('fresh-ku-browser.mjs')),f'http://127.0.0.1:{relay.server_port}',str(out)],env=os.environ,capture_output=True,text=True,timeout=150)
        assert p.returncode == 0, 'browser flow failed; sanitized report in task output'
        return selected[0]
    finally:
        relay.shutdown();relay.server_close()
