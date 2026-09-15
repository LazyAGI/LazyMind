import importlib.util
import io
import json
from pathlib import Path
import unittest
from unittest.mock import patch

spec=importlib.util.spec_from_file_location('bid_client',Path(__file__).parents[1]/'files/scripts/bid_client.py')
client=importlib.util.module_from_spec(spec)
spec.loader.exec_module(client)

class BidTests(unittest.TestCase):
    def test_body_auth_without_url_key(self):
        payload = {'id': 123, 'key': 'untrusted-payload-value'}
        with patch.object(client.urllib.request, 'build_opener') as build:
            build.return_value.open.return_value = io.BytesIO(b'{"code":200,"data":[]}')
            self.assertTrue(client.request('detail', payload, 'test-secret-key')['ok'])
            req = build.return_value.open.call_args.args[0]
            self.assertEqual(req.full_url, 'https://gate.gov-bid.com/outer-gateway/bid/getZTBProjectDetail')
            self.assertNotIn('test-secret-key', req.full_url)
            self.assertEqual(json.loads(req.data)['key'], 'test-secret-key')
            self.assertEqual(payload['key'], 'untrusted-payload-value')


    def args(self,extra=()):
        return client.parser_for_cli().parse_args(['search','--keyword','机器视觉','--start','2026-08-16','--end','2026-09-15',*extra])
    def test_mapping(self):
        p=client.payload_for(self.args(['--area','浙江','--stage','中标信息','--page','2']))
        self.assertEqual((p['areaName'],p['className'],p['pageId'],p['pageNumber']),('浙江','中标信息',2,5))
    def test_bad_range(self):
        a=self.args();a.start='2026-10-01'
        with self.assertRaises(ValueError):client.payload_for(a)
    def test_bad_page(self):
        with self.assertRaises(ValueError):client.payload_for(self.args(['--page','0']))
    def test_missing_key(self):
        with patch.dict('os.environ',{},clear=True):
            self.assertFalse(client.request('search',{})['ok'])
    def test_http_success_business_failure(self):
        with patch.object(client.urllib.request,'build_opener') as m:
            m.return_value.open.return_value=io.BytesIO(b'{"code":403,"msg":"test-key"}')
            result=client.request('search',{},'test-key')
            self.assertFalse(result['ok'])
            self.assertNotIn('test-key',json.dumps(result))
            self.assertEqual(m.return_value.open.call_args.args[0].method,'POST')
    def test_dedup_not_total(self):
        result=dict(ok=True,response=dict(code=200,data=dict(total=100,hasNext=True,data=[dict(id=1,publishTime='x'),dict(id=1,publishTime='x'),dict(id=2,publishTime='y')])) )
        out=client.annotate_search(result,{})
        self.assertEqual(out['selection']['unique_notice_count'],2)
        self.assertEqual(out['selection']['returned_count'],3)
        self.assertEqual(out['selection']['api_total'],100)
    def test_empty_valid(self):
        out=client.annotate_search(dict(ok=True,response=dict(data=dict(data=[],total=0))),{})
        self.assertTrue(out['ok'])
        self.assertEqual(out['selection']['unique_notice_count'],0)
    def test_invalid_shape(self):
        self.assertFalse(client.annotate_search(dict(ok=True,response=dict(data='bad')),{})['ok'])
    def test_redirect(self):
        self.assertIsNone(client.NoRedirect().redirect_request(None,None,302,'',{},'https://example.com'))
    def test_business_error_visible_on_stderr(self):
        err=io.StringIO()
        with patch('sys.argv',['bid_client','detail','--id','1','--published','2026-09-15']), patch.object(client,'request',return_value={'ok':False,'response':{'code':403,'subCode':'0100590006'}}), patch('sys.stderr',err):
            self.assertEqual(client.main(),1)
        self.assertEqual(json.loads(err.getvalue())['response']['subCode'],'0100590006')

if __name__=='__main__':unittest.main()
