#!/usr/bin/env python3
"""Exercise the local demo through nginx. Refuses non-demo or non-localhost origins."""
import http.cookiejar,json,pathlib,urllib.request,urllib.error,uuid,re,os
HOST=os.environ.get('STOREFRONT_SMOKE_HOST','shoptest.localhost');BASE='http://127.0.0.1'
assert re.fullmatch(r'[a-z][a-z0-9-]*\.localhost',HOST),'Local demo hostname required'
class Client:
 def __init__(self):
  self.jar=http.cookiejar.CookieJar();self.http=urllib.request.build_opener(urllib.request.ProxyHandler({}),urllib.request.HTTPCookieProcessor(self.jar));self.csrf=''
 def request(self,path,data=None,method=None,content_type='application/json',csrf=True):
  raw=json.dumps(data).encode() if isinstance(data,(dict,list)) else data
  headers={'Host':HOST,'Origin':'http://'+HOST,'Content-Type':content_type}
  if csrf and self.csrf:headers['X-CSRF-Token']=self.csrf
  req=urllib.request.Request(BASE+path,data=raw,headers=headers,method=method)
  try:
   with self.http.open(req,timeout=15) as response:payload=response.read();status=response.status;kind=response.headers.get('Content-Type','')
  except urllib.error.HTTPError as response:payload=response.read();status=response.status;kind=response.headers.get('Content-Type','')
  return status,json.loads(payload) if 'application/json' in kind else payload
 def login(self,phone):
  status,s=self.request('/api/session');assert status==200 and s['demo'],'refuse live payments/auth'
  status,r=self.request('/api/auth/otp',{'phone':phone});assert status==200,r
  status,r=self.request('/api/auth/login',{'phone':phone,'code':r['demoCode'],'password':''});assert status==200,r
  self.csrf=r['csrf'];return r['user']
client=Client();phone='+989'+str(100000000+int(uuid.uuid4().int%899999999));user=client.login(phone);assert user['role']=='customer'
status,_=client.request('/api/auth/password',{'password':'test-secure-pass'},csrf=False);assert status==403
status,_=client.request('/api/seller/settings',{});assert status==403
status,products=client.request('/api/products');assert status==200
p=next(p for p in products if p['id']=='mug');variant=p['variants'][0];stock=variant['stock']
line={'productId':p['id'],'variantId':variant['id'],'quantity':1,'attachments':[]}
status,r=client.request('/api/quote',{'lines':[line],'code':''});assert status==400,(status,r)
photo=(pathlib.Path(__file__).resolve().parents[1]/'storefront/internal/adapter/web/assets/mug.jpg').read_bytes()
boundary='storefront-'+uuid.uuid4().hex;parts=[]
for key,value in {'productId':p['id'],'variantId':variant['id'],'ruleId':'design'}.items():parts.append(f'--{boundary}\r\nContent-Disposition: form-data; name="{key}"\r\n\r\n{value}\r\n'.encode())
parts.append(f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="design.jpg"\r\nContent-Type: image/jpeg\r\n\r\n'.encode()+photo+b'\r\n');parts.append(f'--{boundary}--\r\n'.encode())
status,upload=client.request('/api/uploads',b''.join(parts),content_type='multipart/form-data; boundary='+boundary);assert status==201,(status,upload);line['attachments']=[upload['id']]
status,q=client.request('/api/quote',{'lines':[line],'code':'HELLOHERA'});assert status==200 and q['discount']>0,(status,q)
address={'name':'مشتری آزمایشی','phone':phone,'province':'تهران','city':'تهران','street':'نشانی آزمایشی، خیابان نمونه، پلاک ۱','postal':'1234567890'}
checkout={'lines':[line],'code':'HELLOHERA','gateway':'demo','key':str(uuid.uuid4()),'address':address}
status,result=client.request('/api/checkout',checkout);assert status==201,(status,result);order=result['order']
status,repeat=client.request('/api/checkout',checkout);assert status==201 and repeat['order']['id']==order['id'],(status,repeat)
for _ in range(2):
 status,result=client.request('/api/payment/demo/'+order['id'],{});assert status==200,(status,result)
status,products=client.request('/api/products');fresh=next(p for p in products if p['id']=='mug');assert fresh['variants'][0]['stock']==stock-1
status,orders=client.request('/api/orders');assert next(o for o in orders if o['id']==order['id'])['paymentStatus']=='paid'
status,data=client.request('/api/uploads/'+upload['id']);assert status==200 and data==photo
other=Client();other.login('+989'+str(100000000+int(uuid.uuid4().int%899999999)))
status,_=other.request('/api/uploads/'+upload['id']);assert status==403
# An OTP-created customer cannot access either management panel.
status,_=other.request('/seller');assert status==403
admin=Client();a=admin.login('+989120000001');assert a['role']=='admin'
status,html=admin.request('/admin');assert status==200 and html.count(b'class="theme-choice"')==16
seller=Client();u=seller.login('+989120000002');assert u['role']=='seller'
status,html=seller.request('/seller');assert status==200 and b'id="launcher-form"' not in html and b'class="theme-choice"' not in html
print(json.dumps({'status':'passed','checks':['OTP login','CSRF','role isolation','required per-item upload','discount','checkout','idempotency','payment verification replay','exact stock movement','private attachment ownership','16 admin styles','seller theme isolation'],'order':order['id']},ensure_ascii=False))
