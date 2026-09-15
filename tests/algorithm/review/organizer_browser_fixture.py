import asyncio
import uvicorn
from fastapi import FastAPI, Request
from lazymind.review.api.preference_organizer_routes import router as organizer
from lazymind.review.api.memory_review_routes import router as review

app = FastAPI()
app.include_router(organizer)
app.include_router(review)


@app.get('/health')
def health():
    return {'ok': True}


@app.middleware('http')
async def fixture_latency(request: Request, next_call):
    # Visible state transitions while the real Core lease and model run normally.
    if request.url.path.endswith('memory_review'):
        await asyncio.sleep(35)
    elif request.url.path.endswith('preference_organize'):
        await asyncio.sleep(12)
    return await next_call(request)


if __name__ == '__main__':
    uvicorn.run(app, host='127.0.0.1', port=18049)
