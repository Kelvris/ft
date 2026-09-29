#!/usr/bin/env python3
"""Local FTP server for ft's acceptance tests (PULL_FIX_PLAN.md §6.2).

Usage: python3 testdata/ftpsrv.py <root-dir> [port]

Serves <root-dir> over FTP on 127.0.0.1 with a single test user
(user/pass).  Read-only permissions are deliberately NOT granted so the
harness can chmod files to exercise transfer failures.
"""

import sys

from pyftpdlib.authorizers import DummyAuthorizer
from pyftpdlib.handlers import FTPHandler
from pyftpdlib.servers import FTPServer


def main() -> int:
    root = sys.argv[1]
    port = int(sys.argv[2]) if len(sys.argv) > 2 else 2121

    authorizer = DummyAuthorizer()
    # 'elradfmwMT': everything, including mkdir/rename/delete, so `ft push`
    # can create directories and remove files.
    authorizer.add_user("user", "pass", root, perm="elradfmwMT")

    handler = FTPHandler
    handler.authorizer = authorizer
    handler.passive_ports = range(60000, 60100)

    server = FTPServer(("127.0.0.1", port), handler)
    server.serve_forever()
    return 0


if __name__ == "__main__":
    sys.exit(main())
