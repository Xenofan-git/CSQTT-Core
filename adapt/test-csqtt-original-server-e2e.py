# Run ONLY against an isolated, disposable CSQTT 2.5.0 instance.
# Never point at production: this test creates and deletes a group.
import base64,json,ssl,urllib.request,urllib.error,http.client,os
base=os.environ['CSQTT_E2E_BASE_URL'].rstrip('/')
ctx=ssl._create_unverified_context()
password=os.environ['CSQTT_E2E_WEB_PASS']
cookie=''
def request(path, method='GET', obj=None, auth=True):
 global cookie
 data=json.dumps(obj).encode() if obj is not None else None
 headers={'Content-Type':'application/json'}
 if auth and cookie: headers['Cookie']=cookie
 req=urllib.request.Request(base+path,data=data,headers=headers,method=method)
 try:
  r=urllib.request.urlopen(req,context=ctx,timeout=10)
  status=r.status; body=r.read(); h=r.headers
 except urllib.error.HTTPError as e:
  status=e.code;body=e.read();h=e.headers
 except (http.client.RemoteDisconnected, ConnectionResetError):
  return 0,"stealth connection closed",{}
 if h.get('Set-Cookie') and 'csqtt_session=' in h.get('Set-Cookie'):
  cookie=h.get('Set-Cookie').split(';')[0]
 try: result=json.loads(body)
 except Exception: result=body.decode('utf8','replace')
 return status,result,h

def check(name,ok):
 print(('PASS' if ok else 'FAIL')+' '+name,flush=True)
 if not ok:raise RuntimeError(name)

s,body,h=request('/api/stats',auth=False)
check('unauthorized stats rejected',s==0 or s!=200 or not isinstance(body,dict))
s,body,h=request('/')
check('unauthorized login HTML',s==200 and isinstance(body,str) and '<!DOCTYPE html>' in body)
encode=lambda v:'c1:'+base64.b64encode(bytes((x+47)%256 for x in v.encode())).decode()
s,body,h=request('/api/login','POST',{'user':encode(os.environ['CSQTT_E2E_WEB_USER']),'pass':encode(password)})
check('login and HttpOnly Secure cookie',s==200 and cookie.startswith('csqtt_session=') and 'HttpOnly' in h.get('Set-Cookie','') and 'Secure' in h.get('Set-Cookie',''))
s,body,h=request('/')
check('authorized full original panel',s==200 and isinstance(body,str) and 'apiDocsDlg' in body and 'cryptoDonateDlg' in body)
s,body,h=request('/api/stats')
check('authorized stats JSON',s==200 and isinstance(body,dict))
s,body,h=request('/api/clients')
check('clients list',s==200 and isinstance(body,list))
s,body,h=request('/api/groups')
check('groups list',s==200 and isinstance(body,list))
s,body,h=request('/api/groups','POST',{'name':'E2E test group'})
check('create group',s==200 and isinstance(body,dict) and body.get('id','').startswith('grp_'))
gid=body['id']
s,body,h=request('/api/groups')
check('group persisted in API',s==200 and any(x.get('id')==gid for x in body))
s,body,h=request('/api/export')
check('export config original format',s==200 and isinstance(body,dict) and body.get('version')==1 and 'groups' in body and 'clients' in body)
export=body
s,body,h=request('/api/import','POST',{'import_clients':False,'import_settings':False,'config':export})
check('import no-op flags',s==200)
s,body,h=request('/api/groups/'+gid,'DELETE')
check('delete group',s==200)
s,body,h=request('/api/groups')
check('deleted group absent',s==200 and all(x.get('id')!=gid for x in body))
for path in ['/api/logs','/api/settings']:
 s,body,h=request(path)
 check('protected '+path,s==200)
print('E2E COMPLETE',flush=True)
