"""S2-A cases consuming the existing S1 host launcher, never another Key writer.

The loopback relay is trusted test instrumentation only. It delays an unchanged
Manager verdict to exercise expiry; it never invents a positive verdict.
"""
import json
import secrets
import time

PATH = "/internal/fresh-chat-admission"
CHAT = "/v1/chat/completions"
BODY = {"model": "s1-proof-model", "stream": False, "messages": [{"role": "user", "content": "private S2-A proof"}]}


def source_paths(root):
    paths = ["bin/ci/fresh_s2a_proof.py", "apps/manager-server/cmd/cpa-manager-plus/fresh_chat_admission.go"]
    paths += [str(p.relative_to(root)) for p in sorted((root/"apps/ingress").rglob("*.go")) if not p.name.endswith("_test.go")]
    return paths


def start_relay(command, probe, replay_test=False):
    program = '''import http.server,http.client,json,time
delay=0;replay=False;last=None
class H(http.server.BaseHTTPRequestHandler):
 def log_message(self,*args): pass
 def do_GET(self): self.forward()
 def do_POST(self): self.forward()
 def do_PUT(self): self.forward()
 def forward(self):
  global delay,replay,last
  b=self.rfile.read(int(self.headers.get('Content-Length',0)))
  if self.path=='/__s2a-proof-delay':
   delay=json.loads(b)['seconds'];self.send_response(204);self.end_headers();return
  if REPLAY_TEST and self.path=='/__s2b-proof-replay':
   replay=json.loads(b)['enabled'];self.send_response(204);self.end_headers();return
  try:
   c=http.client.HTTPConnection('127.0.0.1',18320,timeout=6)
   c.request(self.command,self.path,b,{k:v for k,v in self.headers.items() if k.lower() not in ('host','connection','content-length')})
   r=c.getresponse();data=r.read()
   if REPLAY_TEST and self.path=='/internal/fresh-chat-admission' and r.status==200:
    if replay and last is not None:data=last
    else:last=data
   if self.path=='/internal/fresh-chat-admission': time.sleep(delay)
   self.send_response(r.status)
   for k,v in r.getheaders():
    if k.lower() not in ('connection','transfer-encoding','content-length'):self.send_header(k,v)
   self.send_header('Content-Length',str(len(data)));self.end_headers();self.wfile.write(data);c.close()
  except Exception:
   self.send_response(503);self.end_headers()
http.server.ThreadingHTTPServer(('127.0.0.1',18319),H).serve_forever()
'''
    program = "REPLAY_TEST="+repr(replay_test)+"\n"+program
    command("docker", "exec", "-d", probe, "python3", "-c", program)


def start_meter(run, runtime):
    # Passive loopback packet counter. Official CPA stdout is intentionally
    # discarded by Supervisor, so its Docker log cannot establish arrivals.
    # Keep only path counts, never packet/header/body/credential artifacts.
    program = '''import socket,struct,threading,http.server,json,ctypes
s=socket.socket(socket.AF_PACKET,socket.SOCK_RAW,socket.htons(0x0800))
s.setsockopt(socket.SOL_SOCKET,socket.SO_RCVBUF,8*1024*1024)
# Kernel filter: TCP requests to CPA/Manager only, at most 256 captured bytes.
# Large bootstrap/web responses must never overload the measurement socket.
class Filter(ctypes.Structure):_fields_=[('code',ctypes.c_ushort),('jt',ctypes.c_ubyte),('jf',ctypes.c_ubyte),('k',ctypes.c_uint)]
class Program(ctypes.Structure):_fields_=[('length',ctypes.c_ushort),('filters',ctypes.POINTER(Filter))]
rules=(Filter*8)(*(Filter(*v) for v in [(0x30,0,0,23),(0x15,0,5,6),(0xb1,0,0,14),(0x48,0,0,16),(0x15,1,0,8317),(0x15,0,1,18320),(0x06,0,0,256),(0x06,0,0,0)]))
s.setsockopt(socket.SOL_SOCKET,26,bytes(Program(len(rules),rules)));s.bind(('lo',0))
counts={'cpaChat':0,'cpaTotal':0,'managerVerdicts':0,'dropped':0};seen=set();lock=threading.Lock()
def capture():
 while True:
  p,addr=s.recvfrom(131072)
  if addr[2]==4 or len(p)<54:continue
  ihl=(p[14]&15)*4
  if p[23]!=6:continue
  off=14+ihl;src,dst,seq=struct.unpack('!HHI',p[off:off+8]);tcp=(p[off+12]>>4)*4
  if dst not in (8317,18320):continue
  b=p[off+tcp:]
  if not b.startswith((b'GET ',b'POST ',b'PUT ',b'DELETE ',b'PATCH ',b'HEAD ',b'OPTIONS ')):continue
  line=b.split(b'\\r\\n',1)[0].split(b' ')
  if len(line)!=3 or not line[2].startswith(b'HTTP/'):continue
  with lock:
   marker=(src,dst,seq)
   if marker in seen:continue
   seen.add(marker)
   if dst==8317:
    counts['cpaTotal']+=1
    if line[0]==b'POST' and line[1]==b'/v1/chat/completions':counts['cpaChat']+=1
   if dst==18320 and line[1]==b'/internal/fresh-chat-admission':counts['managerVerdicts']+=1
t=threading.Thread(target=capture,daemon=True);t.start()
class H(http.server.BaseHTTPRequestHandler):
 def log_message(self,*args):pass
 def do_GET(self):
  with lock:
   assert t.is_alive()
   packets,drops=struct.unpack('II',s.getsockopt(263,6,8));counts['dropped']+=drops
   b=json.dumps(counts).encode()
  self.send_response(200);self.send_header('Content-Length',str(len(b)));self.end_headers();self.wfile.write(b)
http.server.ThreadingHTTPServer(('127.0.0.1',18323),H).serve_forever()
'''
    return run("meter", "python:3.13-alpine", "--network", "container:"+runtime,
               "--cap-drop=ALL", "--cap-add=NET_RAW", "--security-opt", "no-new-privileges", "--read-only",
               "--entrypoint", "python3", args=("-c",program))


def counts(command, runtime):
    meter=runtime.removesuffix("-runtime")+"-meter"
    program="import urllib.request;print(urllib.request.urlopen('http://127.0.0.1:18323').read().decode())"
    value=json.loads(command("docker","exec",meter,"python3","-c",program))
    assert value["dropped"]==0,"passive packet counter dropped packets; STOP"
    return value


def cpa_chat_count(command, runtime):
    return counts(command,runtime)["cpaChat"]


def exercise(command, http, record, evidence, name, probe, runtime, ip, token, secret, admin, api, permit, expiry):
    evidence["s2aTrust"] = "S1 Host/Docker authority; private loopback relay delays real verdict bytes only"
    evidence["permitWindow"] = {k: permit[k] for k in ("issuedAtMs", "expiresAtMs", "generation")}
    evidence["notRunStop"] = ["routed IPv6", "external host", "cross-generation recovery", "public Gateway", "default v7 deployment"]

    def check(scenario, key=secret, path=CHAT, body=BODY, expected=503, arrivals=0, port=28471, method="POST"):
        before = len(http(18321, "/events")[1])
        observed_before = counts(command, runtime)
        start = int(time.time()*1000)
        code, _ = http(port, path, method, body, key)
        end = int(time.time()*1000)
        after = len(http(18321, "/events")[1])
        observed_after = counts(command, runtime)
        cpa_arrivals=observed_after["cpaChat"]-observed_before["cpaChat"]
        private_calls=observed_after["managerVerdicts"]-observed_before["managerVerdicts"]
        cpa_total=observed_after["cpaTotal"]-observed_before["cpaTotal"]
        record(scenario=scenario, status=code, cpaChatArrivals=cpa_arrivals, cpaTotalArrivals=cpa_total, managerVerdictArrivals=private_calls,
               upstreamArrivals=after-before, startedAtMs=start, completedAtMs=end,
               runtimeIdentity="embedded-auth-proof", runtimeGeneration=permit["generation"])
        assert code == expected and after-before == arrivals and cpa_arrivals == arrivals, (scenario,code,after-before,cpa_arrivals)
        if expected==404:assert private_calls==0 and cpa_total==0,"private path reached authority or CPA"

    check("installed-canonical-key", expected=200, arrivals=1)
    if expiry:
        remaining = max(0, (permit["expiresAtMs"]-int(time.time()*1000))/1000+0.05)
        time.sleep(remaining)
        code,status = http(9081,"/v1/runtime/status",key=token)
        assert code==200 and status["state"]=="ready" and str(status["runtimeGeneration"])==permit["generation"]
        check("host-permit-expired-with-runtime-still-ready")
        return
    check("no-key", key=None)
    check("wrong-key", key="wrong")
    code, other = http(28471, api+"api-keys", "POST", {"requestId": secrets.token_hex(16)}, admin)
    assert code == 201
    check("other-canonical-staged-not-installed",key=other["createdSecret"])
    check("stream-true",body={**BODY,"stream":True})
    check("other-model",body={**BODY,"model":"other"})
    check("other-model-api",path="/v1/responses")
    check("models-api",path="/v1/models",body=None,method="GET")
    exercise_raw(command,http,record,probe,runtime,secret)
    for path in (PATH, PATH+"/", "/internal/%66resh-chat-admission", "/internal%2ffresh-chat-admission", "/internal%252ffresh-chat-admission", "/x/../internal/fresh-chat-admission", "/control/../internal/fresh-chat-admission"):
        check("public-private-verdict-path",path=path,body={"requestBinding":"a"*64},expected=404)
    # The unchanged real verdict expires between Manager return and CPA write.
    assert http(18319,"/__s2a-proof-delay","POST",{"seconds":1.1})[0]==204
    check("real-manager-verdict-delayed-past-expiry")
    assert http(18319,"/__s2a-proof-delay","POST",{"seconds":0})[0]==204
    check("new-verdict-after-delayed-denial",expected=200,arrivals=1)
    command("docker","stop",name+"-manager")
    check("manager-unavailable")
    command("docker","start",name+"-manager")
    for _ in range(40):
        try:
            if http(28471,api+"operation",key=admin)[0]==200:break
        except RuntimeError:pass
        time.sleep(0.2)
    check("manager-restarted-fresh-admission",expected=200,arrivals=1)
    # Stop only the official child, leaving authenticated Runtime status alive.
    child=command("docker","exec",runtime,"sh","-ec",'''for p in /proc/[0-9]*; do
 [ -r "$p/comm" ] || continue
 [ "$(cat "$p/comm")" = cli-proxy-api ] || continue
 echo "${p##*/}"; exit 0
done
exit 1''')
    assert child.isdigit()
    command("docker","exec","--user","10001:10001",runtime,"sh","-ec",'kill -STOP "$1"',"s2a-stop",child)
    try:check("cpa-child-unresponsive-runtime-still-live")
    finally:command("docker","exec","--user","10001:10001",runtime,"sh","-ec",'kill -CONT "$1"',"s2a-continue",child)
    command("docker","pause",runtime)
    try:check("cpa-and-runtime-unavailable")
    finally:command("docker","unpause",runtime)
    check("same-generation-reachable-again",expected=200,arrivals=1)
    command("docker","restart",name+"-ingress")
    time.sleep(0.5)
    check("ingress-restart-cannot-reuse-old-client-permission",key="wrong")
    check("ingress-restart-must-get-new-verdict",expected=200,arrivals=1)
    check("private-path-after-ingress-restart",path=PATH,body={"requestBinding":"a"*64},expected=404)
    # An untrusted bridge consumer cannot connect to any private authority,
    # including the test relay/backend, nor use a wrong Key at public Ingress.
    program = '''import socket,http.client,json
v=json.loads(input())
for port in (8317,9081,9082,18319,18320,18321):
 s=socket.socket();s.settimeout(1);assert s.connect_ex((v['ip'],port))!=0;s.close()
c=http.client.HTTPConnection(v['ip'],28471,timeout=4)
c.request('POST','/v1/chat/completions',json.dumps(v['body']),{'Content-Type':'application/json','Authorization':'Bearer wrong'})
assert c.getresponse().status==503
'''
    before=cpa_chat_count(command,runtime)
    arrivals=len(http(18321,"/events")[1])
    command("docker","run","--rm","-i","--network",name,"python:3.13-alpine","python3","-c",program,data=json.dumps({"ip":ip,"body":BODY}))
    assert cpa_chat_count(command,runtime)==before and len(http(18321,"/events")[1])==arrivals
    record(scenario="untrusted-bridge-peer",privatePortsRefused=[8317,9081,9082,18319,18320,18321],status=503,cpaChatArrivals=0,upstreamArrivals=0)


def exercise_raw(command,http,record,probe,runtime,secret,body=BODY):
    b=json.dumps(body)
    cases=[
        ("missing-stream",b.replace('"stream": false, ',''),CHAT,"POST",{},False,503),
        ("duplicate-stream",b.replace('"stream": false','"stream":false,"stream":false'),CHAT,"POST",{},False,503),
        ("duplicate-model",b.replace('"model":','"model":"other","model":'),CHAT,"POST",{},False,503),
        ("unicode-folded-stream",b.replace('"stream": false','"stream":false,"\\u017ftream":false'),CHAT,"POST",{},False,503),
        ("escaped-model-alias",b.replace('"model":','"m\\u006fdel":"other","model":'),CHAT,"POST",{},False,503),
        ("duplicate-nested-role",b.replace('"role": "user"','"role":"user","role":"assistant"'),CHAT,"POST",{},False,503),
        ("unpaired-surrogate",b.replace(json.dumps(body["messages"][0]["content"]),'"\\ud800"'),CHAT,"POST",{},False,503),
        ("trailing-json",b+'{}',CHAT,"POST",{},False,503),
        ("over-limit",' '*65537,CHAT,"POST",{},False,503),
        ("compressed",b,CHAT,"POST",{"Content-Encoding":"gzip"},False,503),
        ("chunked",b,CHAT,"POST",{},True,503),
        ("query",b,CHAT+'?x=1',"POST",{},False,503),
        ("encoded-path",b,'/v1/%63hat/completions',"POST",{},False,503),
        ("wrong-method",b,CHAT,"GET",{},False,503),
        ("upgrade",b,CHAT,"POST",{"Connection":"Upgrade","Upgrade":"websocket"},False,503),
        ("invalid-length",b,CHAT,"POST",{"Content-Length":"invalid"},False,400),
    ]
    program='''import socket,http.client,json
v=json.loads(input());results=[]
for label,body,path,method,extra,chunked,expected in v['cases']:
 s=socket.create_connection(('127.0.0.1',28471),timeout=4)
 raw=body.encode();h={'Host':'proof','Connection':'close','Authorization':'Bearer '+v['key'],'Content-Type':'application/json'}
 if chunked:h['Transfer-Encoding']='chunked';raw=hex(len(raw))[2:].encode()+b'\\r\\n'+raw+b'\\r\\n0\\r\\n\\r\\n'
 else:h['Content-Length']=str(len(raw))
 h.update(extra)
 request=(method+' '+path+' HTTP/1.1\\r\\n'+''.join(k+': '+val+'\\r\\n' for k,val in h.items())+'\\r\\n').encode()+raw
 s.sendall(request);r=http.client.HTTPResponse(s);r.begin();code=r.status;r.read();s.close()
 assert code==expected,(label,code)
 results.append({'scenario':label,'status':code})
print(json.dumps(results))
'''
    before=counts(command,runtime)
    arrivals=len(http(18321,"/events")[1])
    results=json.loads(command("docker","exec","-i",probe,"python3","-c",program,data=json.dumps({"cases":cases,"key":secret})))
    after=counts(command,runtime)
    assert after==before and len(http(18321,"/events")[1])==arrivals,"malformed request reached authority/CPA"
    for result in results:record(**result,cpaChatArrivals=0,cpaTotalArrivals=0,managerVerdictArrivals=0,upstreamArrivals=0)


def check_generation_denial(command,http,record,runtime,secret):
    before=cpa_chat_count(command,runtime)
    code,_=http(28471,CHAT,"POST",BODY,secret)
    assert code==503 and cpa_chat_count(command,runtime)==before
    record(scenario="changed-generation-offline-runtime",status=code,cpaChatArrivals=0,upstreamArrivals=0,existingAdminFence=409)
