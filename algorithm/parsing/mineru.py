from lazyllm.tools.servers.mineru.mineru_server_module import MineruServer

from config import config as _cfg

if __name__ == '__main__':
    server = MineruServer(
        port=_cfg['mineru_server_port'],
        default_backend=_cfg['mineru_backend'],
        cache_dir=_cfg['ocr_cache_dir'],
        image_save_dir=_cfg['ocr_cache_dir'],
    )
    server.start()
    server.wait()
