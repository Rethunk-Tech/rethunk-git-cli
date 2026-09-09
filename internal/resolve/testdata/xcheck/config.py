"""Fixture for the pyright binding-line convention."""

import os

CONFIG = {
    "a": 1,
    "b": 2,
}

TOTAL = (
    1
    + 2
)

SINGLE = 3


def validate(tok):
    if not tok:
        raise ValueError("empty")
    return True


class Session:
    def __init__(self, tok):
        self.tok = tok

    def renew(self):
        return Session(self.tok)
