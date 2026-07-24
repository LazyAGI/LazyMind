import os

import uvicorn

from evo.service.api import create_app


app = create_app()


def main() -> None:
    uvicorn.run(
        app,
        host='0.0.0.0',
        port=int(os.getenv('LAZYMIND_EVO_API_PORT', '8047')),
    )


if __name__ == '__main__':
    main()


__all__ = ['app', 'create_app', 'main']
