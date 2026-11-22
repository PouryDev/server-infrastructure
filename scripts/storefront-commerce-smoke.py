#!/usr/bin/env python3
"""Opt-in full commerce demo QA. Modifies only the named .localhost demo store.
Leaves labeled delivery/CMS examples and a completed demo order for inspection.
Never accepts a live storefront or a real payment gateway.
"""
import http.cookiejar
import json
import os
import re
import urllib.error
import urllib.request
import uuid

HOST = os.environ.get('STOREFRONT_SMOKE_HOST', 'shopdemo.localhost')
assert re.fullmatch(r'[a-z][a-z0-9-]*\.localhost', HOST), 'Local demo hostname required'
ORIGIN = 'http://' + HOST


class Client:
    def __init__(self):
        self.csrf = ''
        self.http = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

    def request(self, path, data=None, method=None):
        headers = {'Host': HOST, 'Origin': ORIGIN, 'Content-Type': 'application/json', 'X-CSRF-Token': self.csrf}
        req = urllib.request.Request('http://127.0.0.1' + path, data=None if data is None else json.dumps(data).encode(), headers=headers, method=method)
        try:
            response = self.http.open(req, timeout=20)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            raw = response.read()
            return response.status, json.loads(raw) if 'application/json' in response.headers.get('Content-Type', '') else raw.decode()

    def login(self, phone):
        status, session = self.request('/api/session')
        assert status == 200 and session['demo'], 'Refusing non-demo authentication or payments'
        status, code = self.request('/api/auth/otp', {'phone': phone})
        assert status == 200, (status, code)
        status, session = self.request('/api/auth/login', {'phone': phone, 'code': code['demoCode'], 'password': ''})
        assert status == 200, (status, session)
        self.csrf = session['csrf']
        return session['user']

    def boot(self, path):
        status, page = self.request(path)
        assert status == 200, (path, status)
        return json.loads(re.search(r'<script id="boot" type="application/json">(.*?)</script>', page).group(1))


seller = Client()
assert seller.login('+989120000002')['role'] == 'seller'
settings = seller.boot('/seller')['settings']
settings['deliveryMethods'] = [
    dict(id='post', name='پست پیشتاز — تعرفه نمونه', kind='shipping', carrier='پست', fee=850000, freeAbove=15000000, minDays=2, maxDays=5, enabled=True),
    dict(id='courier', name='پیک تهران — تعرفه نمونه', kind='courier', carrier='پیک', fee=1500000, cities=['تهران'], minDays=0, maxDays=1, enabled=True),
    dict(id='pickup', name='تحویل حضوری نمونه', kind='pickup', fee=0, pickupAddress='نشانی آزمایشی؛ با هماهنگی فروشگاه', minDays=1, maxDays=2, enabled=True),
]
settings['pages'] = [dict(slug='buying-guide', title='راهنمای خرید', description='از انتخاب کالا تا تحویل سفارش', body='۱. محصول و رنگ یا سایز دلخواه را انتخاب کنید.\n۲. روش تحویل را بررسی کنید.\n۳. وضعیت سفارش را در حساب خود دنبال کنید.', published=True), dict(slug='draft-example', title='پیش‌نویس نمونه', description='', body='PRIVATE_DRAFT_QA_CONTENT', published=False)]
settings['navigation'] = [dict(label='راهنمای خرید فروشگاه', url='/pages/buying-guide')]
settings['sections'] = sorted(settings['sections'], key=lambda section: 0 if section['kind'] == 'hero' else 1)
settings['sections'] = [s for s in settings['sections'] if s.get('title') not in ('خدمات فروشگاه', 'پرسش‌های رایج خرید')]
settings['sections'] += [dict(kind='benefits', title='خدمات فروشگاه', enabled=True, items=[dict(title='انتخاب روش تحویل', text='پست، پیک محلی و دریافت حضوری؛ هزینه پیش از پرداخت روشن است.'), dict(title='پیگیری خرید', text='وضعیت آماده‌سازی و کد رهگیری در حساب شما در دسترس است.')]), dict(kind='faq', title='پرسش‌های رایج خرید', enabled=True, items=[dict(title='هزینه ارسال چه زمانی مشخص می‌شود؟', text='پس از تکمیل استان و شهر، روش‌های مناسب و هزینهٔ هرکدام نمایش داده می‌شود.'), dict(title='چطور سفارش را پیگیری کنم؟', text='در حساب کاربری، جزئیات سفارش را باز کنید.')])]
status, out = seller.request('/api/seller/settings', settings)
assert status == 200, (status, out)
guest = Client()
public = guest.boot('/')
assert 'PRIVATE_DRAFT_QA_CONTENT' not in json.dumps(public)
assert guest.request('/pages/buying-guide')[0] == 200
assert guest.request('/pages/draft-example')[0] == 404
assert guest.request('/torob_api/v3/products', {'page': 1, 'sort': 'date_added_desc'})[0] == 401

customer = Client()
phone = '+989' + str(100000000 + uuid.uuid4().int % 899999999)
assert customer.login(phone)['role'] == 'customer'
products = customer.request('/api/products')[1]
product = next(p for p in products if p['id'] == 'ceramic')
variant = product['variants'][0]
line = dict(productId=product['id'], variantId=variant['id'], quantity=1, attachments=[])
for city, method, expected in [('تهران', 'courier', 1500000), ('شیراز', 'post', 850000), ('شیراز', 'pickup', 0)]:
    status, quote = customer.request('/api/quote', dict(lines=[line], code='', delivery=dict(methodId=method, province='تهران', city=city)))
    assert status == 200 and quote['shipping'] == expected and quote['total'] == variant['price'] + expected, (status, quote)
assert customer.request('/api/quote', dict(lines=[line], code='', delivery=dict(methodId='courier', city='شیراز')))[0] == 400
address = dict(name='خریدار آزمایشی', phone=phone, province='تهران', city='تهران', street='خیابان آزمایشی، پلاک یک، واحد یک', postal='1234567890')
checkout = dict(lines=[line], code='', gateway='demo', key=str(uuid.uuid4()), address=address, delivery=dict(methodId='courier'), customerNote='سفارش آزمایشی تست امکانات فروشگاه؛ پرداخت واقعی انجام نشده است.')
status, out = customer.request('/api/checkout', checkout)
assert status == 201, (status, out)
order = out['order']
assert order['quote']['shipping'] == 1500000
assert customer.request('/api/checkout', checkout)[1]['order']['id'] == order['id']
assert customer.request('/api/payment/demo/' + order['id'], {})[0] == 200
status, out = seller.request('/api/seller/orders/' + order['id'] + '/shipment', dict(carrier='پیک آزمایشی', trackingCode='DEMO-123456', note='رهگیری نمونه؛ حمل واقعی انجام نشده است.'))
assert status == 200, (status, out)
assert seller.request('/api/seller/orders/' + order['id'], dict(status='completed'))[0] == 200
assert customer.request('/api/reviews', dict(productId=product['id'], rating=5, title='نظر آزمایشی خریدار تأییدشده', body='این نظر برای بررسی چرخهٔ خرید و مدیریت نظرات ثبت شده است.'))[0] == 201
review = next(r for r in seller.request('/api/seller/reviews')[1] if r['title'] == 'نظر آزمایشی خریدار تأییدشده' and r['status'] == 'pending')
assert seller.request('/api/seller/reviews/' + review['id'], dict(status='published'))[0] == 200
assert customer.request('/api/customer/returns', dict(orderId=order['id'], reason='درخواست آزمایشی برای بررسی مسیر رسیدگی به بازگشت کالا'))[0] == 201
for state in ('approved', 'received', 'closed'):
    status, out = seller.request('/api/seller/returns/' + order['id'], dict(status=state, note='رسیدگی آزمایشی؛ هیچ بازپرداخت بانکی یا جابه‌جایی واقعی کالا انجام نشده است.'))
    assert status == 200, (status, out)
assert customer.request('/account/orders/' + order['id'])[0] == 200
for theme in ('catalog', 'orbit', 'pulse', 'mono', 'bazaar', 'street', 'atelier', 'play', 'journal', 'terminal', 'mode', 'bloom', 'volt', 'fresh', 'little', 'craft'):
    status, page = guest.request('/?preview=' + theme)
    assert status == 200 and 'data-style="' + theme + '"' in page and 'ZgotmplZ' not in page, (theme, status)
    assert 'class="desktop-showcase showcase-' + theme + '"' in page
status, page = guest.request('/products/' + product['slug'] + '?variant=' + variant['id'])
assert status == 200 and 'ProductGroup' in page and 'name="product_id" content="' + variant['id'] + '"' in page
assert 'class="color-swatch"' in page and 'نظر آزمایشی خریدار تأییدشده' in page
assert seller.request('/api/seller/torob')[0] == 200
print(json.dumps(dict(status='passed', host=HOST, order=order['id'], checks=['16 SSR themes', 'CMS publication and draft privacy', 'delivery tariffs and city restriction', 'delivery snapshot', 'checkout replay', 'demo payment', 'shipment tracking', 'verified review moderation', 'return lifecycle', 'variant metadata and ProductGroup', 'Torob authentication boundary']), ensure_ascii=False))
