#!/usr/bin/env python3
"""Stored moderation settings across the 0.11.0 contract release (CONTRACTS §72, 0.11.0).

The plugin cannot rewrite its own stored settings, so this does it through the
core's admin API, the way the console does. Standard library only. Only the
rollback to 0.10.0 is left: the 0.11.0 release step that cleaned the stored
settings read the keys to keep from the settings schema, which the plugin no
longer has since 0.12.2 (it would have stored empty settings).

  rollback-prepare   before rolling back to 0.10.0 (run under 0.11.0): export the
                     locked entries to --export, then turn each into a plain,
                     non-expiring entry of the same level, so 0.10.0 (which locks
                     from the settings lists) neither unlocks nor drops them.
  rollback-settings  right after the plugin is back on 0.10.0: write the exported
                     lists into allowlist_user_ids / blocklist_user_ids (0.10.0
                     locks them again) and ban_window_seconds back into
                     ban_window_hours; keys 0.10.0 does not declare are dropped.

usage: settings_cleanup.py rollback-prepare|rollback-settings --base http://<ip>:8080 --token <admin token>
       [--export locks.json] [--dry-run]
Masked secrets go back as the mask, which the core resolves to the stored value.
Exit code 0 when the step succeeded and was verified.
"""
import argparse
import json
import math
import sys
import urllib.error
import urllib.request

SETTINGS = "/api/v1/plugins/moderation/settings"
ROUTES = "/api/v1/p/moderation"

class Api:
    def __init__(self, base, token):
        self.base, self.token = base.rstrip("/"), token

    def __call__(self, method, path, body=None):
        req = urllib.request.Request(self.base + path, method=method,
                                     data=None if body is None else json.dumps(body).encode(),
                                     headers={"Authorization": "Bearer " + self.token, "Content-Type": "application/json"})
        try:
            with urllib.request.urlopen(req, timeout=30) as r:
                return json.load(r)
        except urllib.error.HTTPError as e:
            sys.exit(f"{method} {path}: HTTP {e.code} {e.read().decode(errors='replace')[:500]}")

    def settings(self):
        return self("GET", SETTINGS)["data"]

    def put_verified(self, values, gone):
        """PUT values, then check that the stored settings are exactly values without the keys of gone."""
        self("PUT", SETTINGS, {"values": values})
        after = self.settings()["values"]
        problems = [f"{k} still stored" for k in gone if k in after]
        problems += [f"{k}: {after.get(k)!r}, want {v!r}" for k, v in values.items() if after.get(k) != v]
        problems += [f"{k} appeared" for k in set(after) - set(values)]
        for line in problems:
            print("FAIL " + line)
        print(f"stored keys {len(after)}; " + ("verified" if not problems else f"{len(problems)} problem(s)"))
        return 1 if problems else 0


def declared(view):
    """The keys the installed settings schema declares (0.10.0 has one)."""
    return set((view.get("schema") or {}).get("properties") or {})


def number(v):
    return isinstance(v, (int, float)) and not isinstance(v, bool)


def locked_entries(api):
    out, page = [], 1
    while True:
        r = api("GET", f"{ROUTES}/watchlist?locked=true&page={page}&page_size=100")
        out += [{"user_id": w["user_id"], "level": w["level"], "lock": w["lock"]} for w in r["data"]]
        if page * 100 >= r["page"]["total"]:
            return out
        page += 1


def rollback_prepare(api, a):
    entries = locked_entries(api)
    print(f"{len(entries)} locked entr{'y' if len(entries) == 1 else 'ies'}")
    if a.dry_run:
        return 0
    with open(a.export, "w") as f:
        json.dump(entries, f, indent=1)
    for e in entries:
        api("DELETE", f"{ROUTES}/watchlist/{e['user_id']}/lock")
        api("POST", f"{ROUTES}/watchlist", {"user_id": e["user_id"], "level": e["level"], "days": 0,
                                            "reason": f"rollback to 0.10.0: was locked ({e['lock']})"})
    left = locked_entries(api)
    print(f"exported to {a.export}; locked entries left: {len(left)}")
    return 1 if left else 0


def rollback_settings(api, a):
    with open(a.export) as f:
        entries = json.load(f)
    view = api.settings()
    keep, stored = declared(view), view.get("values") or {}
    if "allowlist_user_ids" not in keep:
        sys.exit("the installed schema has no allowlist_user_ids: run this after the rollback to 0.10.0")
    values = {k: v for k, v in stored.items() if k in keep}
    for lst in ("allowlist", "blocklist"):
        values[f"{lst}_user_ids"] = [str(e["user_id"]) for e in entries if e["lock"] == lst]
    if number(stored.get("ban_window_seconds")) and "ban_window_hours" not in stored:
        values["ban_window_hours"] = min(8760, max(1, math.ceil(stored["ban_window_seconds"] / 3600)))
    gone = sorted(set(stored) - keep)
    print(f"lists: allow {len(values['allowlist_user_ids'])}, block {len(values['blocklist_user_ids'])}; "
          f"ban_window_hours {values.get('ban_window_hours')}; drop {', '.join(gone) or '-'}")
    return 0 if a.dry_run else api.put_verified(values, gone)


def main():
    p = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    p.add_argument("step", choices=("rollback-prepare", "rollback-settings"))
    p.add_argument("--base", required=True, help="core address, e.g. http://10.0.0.5:8080")
    p.add_argument("--token", required=True, help="access token of an administrator (plugin:manage, moderation:manage)")
    p.add_argument("--export", default="moderation-locks.json", help="locked entries file of the rollback steps")
    p.add_argument("--dry-run", action="store_true", help="only print what would change")
    a = p.parse_args()
    step = {"rollback-prepare": rollback_prepare, "rollback-settings": rollback_settings}[a.step]
    return step(Api(a.base, a.token), a)


if __name__ == "__main__":
    sys.exit(main())
