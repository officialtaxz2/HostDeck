#!/usr/bin/env python3
"""Leitet TCP von einer Adresse am Labor-Netz an eine andere weiter.

Nur für das Prüflabor: Hat die Bauumgebung einen Proxy nur auf 127.0.0.1,
erreichen ihn die Labor-Server so über das Gateway des Netzes.
Aufruf: weiterleiter.py <horch-ip> <horch-port> <ziel-ip> <ziel-port>
"""
import asyncio
import sys


async def pipe(r, w):
    try:
        while True:
            b = await r.read(65536)
            if not b:
                break
            w.write(b)
            await w.drain()
    except Exception:
        pass
    finally:
        try:
            w.close()
        except Exception:
            pass


async def main(lh, lp, th, tp):
    async def handle(r, w):
        try:
            tr, tw = await asyncio.open_connection(th, tp)
        except Exception:
            w.close()
            return
        await asyncio.gather(pipe(r, tw), pipe(tr, w))

    srv = await asyncio.start_server(handle, lh, lp)
    async with srv:
        await srv.serve_forever()


if __name__ == "__main__":
    asyncio.run(main(sys.argv[1], int(sys.argv[2]), sys.argv[3], int(sys.argv[4])))
