"""OFFLINE S2-B integration cases for the existing disposable S1 launcher.

No real Provider, credential, external network or S2-B qualification. The TLS
stub exists only in this task's internal namespace; no product TLS bypass.
"""
import hashlib
import json
import pathlib
import secrets
import subprocess
import tempfile
import time

MODEL = "s2b-offline-model"
HOST = "s2b-provider.invalid"
TARGET = "https://" + HOST + "/v1"
BODY = {"model": MODEL, "stream": False, "messages": [{"role": "user", "content": "offline integration only"}]}


def fixture():
    credential = secrets.token_hex(32)
    return {"provider": "s2b-offline", "model": MODEL, "target": TARGET,
            "credentialSHA256": hashlib.sha256(credential.encode()).hexdigest()}, credential


def seed_config(management, identity, credential):
    return json.dumps({
        "config-version": 8, "server": {"host": "127.0.0.1", "port": 8317},
        "management": {"allow-remote": False, "secret-key": management,
                       "disable-control-panel": True, "disable-auto-update-panel": True},
        "access": {"api-keys": []}, "oauth": {"auth-dir": "/runtime/gateway/auth"},
        "plugins": {"enabled": False, "dir": "/runtime/gateway/plugins"},
        "observability": {"logs": {"logging-to-file": False}, "usage": {"usage-statistics-enabled": False}},
        "api-keys": {"openai-compatibility": [{"name": identity["provider"], "base-url": identity["target"],
            "request-retry": 0, "keys": [{"api-key": credential}],
            "models": [{"name": identity["model"], "alias": identity["model"]}]}]}})


def start_offline_tls(command, runtime, probe, credential):
    # New temporary CA for an explicitly synthetic .invalid destination. The
    # official CPA still performs ordinary certificate and hostname validation.
    with tempfile.TemporaryDirectory(prefix="cpamp-s2b-offline-") as directory:
        p = pathlib.Path(directory)
        result = subprocess.run(["openssl", "req", "-x509", "-nodes", "-newkey", "rsa:2048", "-days", "1",
            "-subj", "/CN="+HOST, "-addext", "subjectAltName=DNS:"+HOST,
            "-keyout", str(p/"key"), "-out", str(p/"cert")], capture_output=True, timeout=30)
        assert result.returncode == 0, "offline TLS certificate creation failed"
        cert, key = (p/"cert").read_text(), (p/"key").read_text()
    command("docker", "exec", "-i", runtime, "sh", "-ec",
            "cat > /usr/local/share/ca-certificates/s2b-offline.crt; update-ca-certificates >/dev/null 2>&1", data=cert)
    # Input and ephemeral files stay in the trusted tool's tmpfs. No credentials
    # or private key are placed in argv, logs or evidence.
    setup = '''import json,sys,pathlib,subprocess
v=json.load(sys.stdin)
pathlib.Path('/tmp/cert').write_text(v['cert']);pathlib.Path('/tmp/key').write_text(v['key'])
pathlib.Path('/tmp/server.py').write_text(v['program'])
child=subprocess.Popen(['python3','/tmp/server.py'],stdin=subprocess.PIPE,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,start_new_session=True)
child.stdin.write((json.dumps(v['credential'])+'\\n').encode());child.stdin.close()
'''
    server = '''import http.server,ssl,json,sys
credential=json.loads(sys.stdin.readline())
events=[];failure=False
class H(http.server.BaseHTTPRequestHandler):
 def log_message(self,*args):pass
 def do_GET(self):
  b=json.dumps(events).encode();self.send_response(200);self.end_headers();self.wfile.write(b)
 def do_POST(self):
  global failure
  if self.path=='/fail':failure=True;self.send_response(204);self.end_headers();return
  if self.headers.get('Authorization')!='Bearer '+credential:self.send_response(401);self.end_headers();return
  value=json.loads(self.rfile.read(int(self.headers['Content-Length'])))
  assert self.path=='/v1/chat/completions' and value['model']=='s2b-offline-model' and value['stream'] is False
  events.append({'path':self.path,'model':value.get('model'),'stream':value.get('stream')})
  b=json.dumps({'id':'offline-stub-only','object':'chat.completion','model':'s2b-offline-model','choices':[{'index':0,'message':{'role':'assistant','content':'OFFLINE STUB; NOT REAL PROVIDER'},'finish_reason':'stop'}]}).encode()
  self.send_response(503 if failure else 200);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(b)));self.end_headers();self.wfile.write(b)
import threading
threading.Thread(target=http.server.ThreadingHTTPServer(('127.0.0.1',18321),H).serve_forever,daemon=True).start()
s=http.server.ThreadingHTTPServer(('127.0.0.1',443),H)
c=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);c.load_cert_chain('/tmp/cert','/tmp/key');s.socket=c.wrap_socket(s.socket,server_side=True);s.serve_forever()
'''
    command("docker", "exec", "-i", probe, "python3", "-c", setup, data=json.dumps({"cert":cert,"key":key,"program":server,"credential":credential}))
    time.sleep(0.3)


def check(command, http, record, runtime, scenario, key, body=BODY, expected=503, arrivals=0, path="/v1/chat/completions", port=28471):
    from fresh_s2a_proof import counts
    before = counts(command, runtime)
    upstream = len(http(18321, "/events")[1])
    code, response = http(port, path, "POST", body, key)
    after = counts(command, runtime)
    delta = after["cpaChat"]-before["cpaChat"]
    upstream_delta = len(http(18321, "/events")[1])-upstream
    record(scenario=scenario, status=code, cpaChatArrivals=delta, offlineStubArrivals=upstream_delta)
    assert code == expected and delta == arrivals and upstream_delta == arrivals, (scenario, code, delta, upstream_delta)
    return response


def exercise(command, http, record, evidence, runtime, probe, name, secret, admin, api, expiry, permit, token):
    from fresh_s2a_proof import exercise_raw, counts
    response = check(command,http,record,runtime,"OFFLINE-TLS-stub-through-official-CPA",secret,expected=200,arrivals=1)
    assert response["id"] == "offline-stub-only" and response["model"] == MODEL
    assert http(18319,"/__s2b-proof-replay","POST",{"enabled":True})[0] == 204
    check(command,http,record,runtime,"previous-verdict-same-body-new-nonce",secret)
    check(command,http,record,runtime,"previous-verdict-different-body",secret,{**BODY,"messages":[{"role":"user","content":"different offline request"}]})
    assert http(18319,"/__s2b-proof-replay","POST",{"enabled":False})[0] == 204
    evidence["liveProvider"] = "LIVE NOT RUN / HOLD"
    evidence["controlledExternalEgress"] = "NOT RUN / HOLD; this run uses internal network only"
    evidence["permitWindow"] = {k:permit[k] for k in ("generation","issuedAtMs","expiresAtMs")}
    if expiry:
        while time.time()*1000 <= permit["expiresAtMs"]: time.sleep(min(1,max(0.01,(permit["expiresAtMs"]-time.time()*1000)/1000)))
        code, current = http(9081,"/v1/runtime/status",key=token)
        assert code == 200 and current["state"] == "ready" and str(current["runtimeGeneration"]) == permit["generation"]
        evidence["readyStatusAfterPermitExpiry"] = current
        check(command,http,record,runtime,"expired-permit-ready-generation",secret)
        return
    for label,key in (("absent-key",None),("wrong-key","wrong"),("wrong-canonical-shaped-key","cpamp_sk_"+"0"*64)):
        check(command,http,record,runtime,label,key)
    code, other = http(28471,api+"api-keys","POST",{"requestId":secrets.token_hex(16)},admin)
    assert code == 201
    check(command,http,record,runtime,"staged-only-other-key",other["createdSecret"])
    check(command,http,record,runtime,"streaming",secret,{**BODY,"stream":True})
    check(command,http,record,runtime,"wrong-model",secret,{**BODY,"model":"s1-proof-model"})
    check(command,http,record,runtime,"other-api",secret,path="/v1/responses")
    before = counts(command,runtime)
    assert http(9081,"/v1/runtime/status",key="wrong")[0] == 401
    assert counts(command,runtime) == before
    record(scenario="runtime-unauthorized-loopback",status=401,cpaChatArrivals=0)
    # Reuse raw/URL/JSON framing cases with the new fixed model, so malformed
    # JSON rejection is exercised rather than merely failing model selection.
    exercise_raw(command,http,record,probe,runtime,secret,body=BODY)
    for path in ("/internal/fresh-chat-admission","/internal/%66resh-chat-admission","/internal%252ffresh-chat-admission"):
        check(command,http,record,runtime,"public-private-verdict",secret,path=path,expected=404)
    ip = json.loads(command("docker","inspect",runtime))[0]["NetworkSettings"]["Networks"][name]["IPAddress"]
    before = counts(command,runtime)
    program = '''import socket,http.client,json,sys
v=json.load(sys.stdin)
for port in (443,8317,9081,9082,18319,18320,18321):
 s=socket.socket();s.settimeout(1);assert s.connect_ex((v['ip'],port))!=0;s.close()
c=http.client.HTTPConnection(v['ip'],28471,timeout=4)
c.request('POST','/internal/fresh-chat-admission','{}',{'Content-Type':'application/json'})
assert c.getresponse().status==404
c=http.client.HTTPConnection(v['ip'],28471,timeout=4)
c.request('POST','/v1/chat/completions',json.dumps(v['body']),{'Content-Type':'application/json','Authorization':'Bearer wrong'})
assert c.getresponse().status==503
'''
    command("docker","run","--rm","-i","--network",name,"python:3.13-alpine","python3","-c",program,data=json.dumps({"ip":ip,"body":BODY}))
    assert counts(command,runtime) == before
    record(scenario="OFFLINE-untrusted-bridge-peer",privatePortsRefused=[443,8317,9081,9082,18319,18320,18321],cpaChatArrivals=0)
    assert http(18319,"/__s2a-proof-delay","POST",{"seconds":1.1})[0] == 204
    check(command,http,record,runtime,"expired-real-verdict",secret)
    assert http(18319,"/__s2a-proof-delay","POST",{"seconds":0})[0] == 204
    command("docker","stop",name+"-manager")
    try: check(command,http,record,runtime,"manager-unavailable",secret)
    finally: command("docker","start",name+"-manager")
    for _ in range(40):
        try:
            if http(28471,api+"operation",key=admin)[0] == 200: break
        except RuntimeError: pass
        time.sleep(0.2)
    child=command("docker","exec",runtime,"sh","-ec",'''for p in /proc/[0-9]*; do
 [ -r "$p/comm" ] || continue
 [ "$(cat "$p/comm")" = cli-proxy-api ] || continue
 echo "${p##*/}"; exit 0
done
exit 1''')
    assert child.isdigit()
    command("docker","exec","--user","10001:10001",runtime,"sh","-ec",'kill -STOP "$1"',"offline-stop",child)
    try: check(command,http,record,runtime,"CPA-unresponsive-runtime-live",secret)
    finally: command("docker","exec","--user","10001:10001",runtime,"sh","-ec",'kill -CONT "$1"',"offline-continue",child)
    # Indeterminate/failing upstream response remains a real attempt, exactly
    # once at both CPA and the TLS stub. No retry by Ingress or official CPA.
    assert http(18321,"/fail","POST",{})[0] == 204
    check(command,http,record,runtime,"upstream-503-no-retry",secret,expected=503,arrivals=1)
    command("docker","pause",runtime)
    try: check(command,http,record,runtime,"runtime-and-CPA-unavailable",secret)
    finally: command("docker","unpause",runtime)
    evidence["offlineCounter"] = counts(command,runtime)
