import http.server,threading,tempfile,os,json,sqlite3,socket
from pathlib import Path
from concurrent.futures import ThreadPoolExecutor
from lazymind.common.database.sqlite_proxy import Connection
ports=[]; dropped=[]; lock=threading.Lock()
class H(http.server.BaseHTTPRequestHandler):
 protocol_version='HTTP/1.1'
 def do_POST(self):
  self.rfile.read(int(self.headers['Content-Length']))
  with lock: ports.append(self.client_address[1])
  if self.path=='/drop':
   dropped.append(1);self.close_connection=True;self.connection.shutdown(socket.SHUT_RDWR);self.connection.close();return
  self.send_response(200);self.send_header('Content-Length','2');self.end_headers();self.wfile.write(b'{}');self.wfile.flush()
 def log_message(self,*a):pass
s=http.server.ThreadingHTTPServer(('127.0.0.1',0),H);s.daemon_threads=True
threading.Thread(target=s.serve_forever,daemon=True).start()
try:
 with tempfile.TemporaryDirectory() as td:
  p=Path(td)/'token';p.write_text('offline-probe')
  os.environ['LAZYMIND_SQLITE_SERVER_URL']=f'http://127.0.0.1:{s.server_port}'
  os.environ['LAZYMIND_SQLITE_SERVER_TOKEN_FILE']=str(p)
  c=Connection('segments')
  def run(i):return c._call('/v1/query')
  with ThreadPoolExecutor(max_workers=8) as pool:assert all(x=={} for x in pool.map(run,range(100)))
  out={'concurrent_calls':100,'worker_threads':8,'same_connection_client_ports':len(set(ports))}
  try:c._call('/drop');raise AssertionError('expected transport error')
  except sqlite3.OperationalError:pass
  out['disconnect_request_count_no_auto_retry']=len(dropped)
  out['next_explicit_call_reconnects']=c._call('/v1/query')=={}
  c.close()
  assert out['same_connection_client_ports']==1 and len(dropped)==1
  Path('/tmp/report-retest-1026/http.json').write_text(json.dumps(out,indent=2));print(out)
finally:s.shutdown();s.server_close()
