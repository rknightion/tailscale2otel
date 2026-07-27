"""value-mapping constants shared across tabs (moved out of build.py in the module split)."""

from builder import vmap


UP_MAP = vmap({"0": {"text": "DOWN", "color": "red", "index": 0},
               "1": {"text": "UP", "color": "green", "index": 1}})
BOOL_MAP = vmap({"0": {"text": "off", "color": "red", "index": 0},
                 "1": {"text": "on", "color": "green", "index": 1}})
