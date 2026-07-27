"""Value-mapping constants shared across tabs (moved out of build.py in the module split).

A boolean flag has no universal polarity: "1" is the healthy state for a feature
switch and the unhealthy one for a risk indicator. There used to be one shared
BOOL_MAP hardcoding 0=red/"off", 1=green/"on", which rendered every inverse-risk
panel backwards — a healthy "no SSH wildcard in the ACL" showed as a red "off"
(#385). It is deliberately GONE rather than aliased: a name that does not state its
polarity is how that bug comes back. Pick the map that matches the panel's meaning,
and keep it consistent with the panel's thresholds — panel() enforces that.
"""

from builder import vmap


def bool_map(off_text="off", on_text="on", off_color="red", on_color="green"):
    """Custom 0/1 value map, for flags the three constants below do not describe."""
    return vmap({"0": {"text": off_text, "color": off_color, "index": 0},
                 "1": {"text": on_text, "color": on_color, "index": 1}})


UP_MAP = bool_map("DOWN", "UP", "red", "green")

# 1 is the healthy state (a feature is on, config is valid).
BOOL_HEALTHY_ON = bool_map()

# 0 is the healthy state — inverse risk (a wildcard rule exists, an error is latched).
BOOL_HEALTHY_OFF = bool_map(off_color="green", on_color="red")

# Neither value is good or bad; render the state without a verdict.
BOOL_NEUTRAL = bool_map(off_color="text", on_color="text")
