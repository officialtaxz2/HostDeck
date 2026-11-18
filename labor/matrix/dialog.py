#!/usr/bin/env python3
"""Spielt einen Dialog am Terminal durch wie ein Mensch.

    labor/matrix/dialog.py CONTAINER BEFEHL FRAGE=ANTWORT …

Startet BEFEHL mit `docker exec -it` in CONTAINER (mit echtem Terminal),
wartet nacheinander auf jede FRAGE (regulärer Ausdruck) und tippt dann
ANTWORT und Enter. Am Ende muss der Befehl mit Code 0 enden. Die ganze
Ausgabe geht nach STDOUT.
"""
import os
import pty
import re
import select
import sys
import time

TIMEOUT = 300


def main() -> int:
    if len(sys.argv) < 3:
        print(__doc__, file=sys.stderr)
        return 2
    container, cmd, steps = sys.argv[1], sys.argv[2], sys.argv[3:]
    pairs = []
    for s in steps:
        q, sep, a = s.partition("=")
        if not sep:
            print(f"Schritt ohne =: {s}", file=sys.stderr)
            return 2
        pairs.append((re.compile(q), a))
    pid, fd = pty.fork()
    if pid == 0:
        os.execvp("docker", ["docker", "exec", "-it", container, "bash", "-c", cmd])
    seen = ""
    pos = 0
    deadline = time.time() + TIMEOUT
    i = 0
    while True:
        if time.time() > deadline:
            print(f"\n[dialog] Zeit abgelaufen, wartete auf: {pairs[i][0].pattern if i < len(pairs) else 'Ende'}")
            os.kill(pid, 9)
            return 1
        r, _, _ = select.select([fd], [], [], 1)
        if r:
            try:
                chunk = os.read(fd, 4096).decode("utf-8", "replace")
            except OSError:
                break
            if not chunk:
                break
            sys.stdout.write(chunk)
            sys.stdout.flush()
            seen += chunk
        while i < len(pairs):
            m = pairs[i][0].search(seen, pos)
            if not m:
                break
            pos = m.end()
            time.sleep(0.3)
            os.write(fd, (pairs[i][1] + "\r").encode())
            i += 1
    _, status = os.waitpid(pid, 0)
    code = os.waitstatus_to_exitcode(status)
    if i < len(pairs):
        print(f"\n[dialog] Frage kam nicht: {pairs[i][0].pattern}")
        return 1
    print(f"\n[dialog] Ende mit Code {code}")
    return 0 if code == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
