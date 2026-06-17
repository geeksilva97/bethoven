#!/usr/bin/env python3
"""Generate the per-team recent-form baseline in BEThoven's fixtures.json.

Source: ESPN's keyless soccer scoreboard (site.api.espn.com) — no API key. These
are unofficial endpoints and can change without notice; if a slug 404s or the
shape shifts, this script skips it rather than failing.

How it works:
  1. fifa.world/teams              -> the 48 World Cup teams + their ESPN ids.
  2. {slug}/scoreboard?dates=A-B   -> completed international matches (friendlies,
     Nations League, qualifiers), joined to teams by ESPN id.
  3. For each WC team, take its last 5 completed matches -> a "WDL" string
     (left = oldest, right = newest), from that team's perspective.
  4. Write a "teams" section into fixtures.json (matches are preserved).

The server reads this baseline at boot and merges real tournament results on top
at read time, so re-running this is only needed to refresh pre-tournament form.

Usage:
    python3 scripts/build_form.py            # ~12-month window ending today
    python3 scripts/build_form.py 2025-06-01 # custom window start (to today)
"""

import json
import sys
import urllib.request
from datetime import date, datetime, timedelta, timezone

BASE = "https://site.api.espn.com/apis/site/v2/sports/soccer"
OUT_PATH = "fixtures.json"
LAST_N = 5

# International competition slugs to aggregate. Club leagues are intentionally
# excluded — national-team form should reflect internationals only.
SLUGS = [
    "fifa.friendly",
    "uefa.nations",
    "concacaf.nations",
    "fifa.worldq.uefa",
    "fifa.worldq.conmebol",
    "fifa.worldq.concacaf",
    "fifa.worldq.afc",
    "fifa.worldq.caf",
    "fifa.worldq.ofc",
    "fifa.worldq.intercontinental",
]

# ESPN display name -> fixtures.json team name, for the few that differ.
ALIASES = {
    "United States": "USA",
    "Türkiye": "Turkey",
    "Korea Republic": "South Korea",
    "South Korea": "South Korea",
    "IR Iran": "Iran",
    "Côte d'Ivoire": "Ivory Coast",
    "Cabo Verde": "Cape Verde",
    "Bosnia-Herzegovina": "Bosnia & Herzegovina",
    "Czechia": "Czech Republic",
    "Congo DR": "DR Congo",
}


def fetch(url):
    req = urllib.request.Request(url, headers={"User-Agent": "bethoven-build-form"})
    with urllib.request.urlopen(req, timeout=30) as r:
        return json.load(r)


def wc_teams():
    """Return {espn_id: espn_name} for the 48 World Cup teams."""
    data = fetch(f"{BASE}/fifa.world/teams")
    out = {}
    for sport in data.get("sports", []):
        for league in sport.get("leagues", []):
            for entry in league.get("teams", []):
                t = entry.get("team", {})
                if t.get("id") and t.get("displayName"):
                    out[str(t["id"])] = t["displayName"]
    # Some payloads nest teams directly under "teams".
    for entry in data.get("teams", []):
        t = entry.get("team", {})
        if t.get("id") and t.get("displayName"):
            out[str(t["id"])] = t["displayName"]
    return out


def month_chunks(start, end):
    """Yield (from, to) YYYYMMDD strings, one calendar month at a time."""
    cur = start
    while cur <= end:
        nxt = (cur.replace(day=28) + timedelta(days=4)).replace(day=1)
        hi = min(nxt - timedelta(days=1), end)
        yield cur.strftime("%Y%m%d"), hi.strftime("%Y%m%d")
        cur = nxt


def collect(team_ids, start, end):
    """Return {team_id: [(date, my_score, opp_score), ...]} for completed games."""
    results = {tid: [] for tid in team_ids}
    for slug in SLUGS:
        for lo, hi in month_chunks(start, end):
            url = f"{BASE}/{slug}/scoreboard?dates={lo}-{hi}&limit=200"
            try:
                data = fetch(url)
            except Exception as e:  # noqa: BLE001 — best-effort; skip bad slugs/ranges
                print(f"  skip {slug} {lo}-{hi}: {e}", file=sys.stderr)
                continue
            for ev in data.get("events", []):
                comps = ev.get("competitions") or []
                if not comps:
                    continue
                comp = comps[0]
                status = (comp.get("status") or {}).get("type") or {}
                if not status.get("completed"):
                    continue
                cs = comp.get("competitors") or []
                if len(cs) != 2:
                    continue
                try:
                    a_id = str(cs[0]["team"]["id"])
                    b_id = str(cs[1]["team"]["id"])
                    a_sc = int(cs[0]["score"])
                    b_sc = int(cs[1]["score"])
                except (KeyError, ValueError, TypeError):
                    continue
                when = ev.get("date", "")
                if a_id in results:
                    results[a_id].append((when, a_sc, b_sc))
                if b_id in results:
                    results[b_id].append((when, b_sc, a_sc))
    return results


def form_string(games):
    games = sorted(games, key=lambda g: g[0])[-LAST_N:]  # oldest -> newest
    out = []
    for _, mine, opp in games:
        out.append("W" if mine > opp else "L" if mine < opp else "D")
    return "".join(out)


def main():
    start = date.today() - timedelta(days=365)
    if len(sys.argv) > 1:
        start = datetime.strptime(sys.argv[1], "%Y-%m-%d").date()
    end = date.today()
    print(f"window {start} .. {end}", file=sys.stderr)

    id_to_espn = wc_teams()
    print(f"WC teams: {len(id_to_espn)}", file=sys.stderr)

    with open(OUT_PATH, encoding="utf-8") as f:
        fixtures = json.load(f)

    # The names we must produce keys for (from the actual fixtures).
    fixture_names = set()
    for m in fixtures.get("matches", []):
        fixture_names.add(m["team_a"])
        fixture_names.add(m["team_b"])

    raw = collect(set(id_to_espn), start, end)

    teams = []
    matched = 0
    for tid, espn_name in sorted(id_to_espn.items(), key=lambda kv: kv[1]):
        name = ALIASES.get(espn_name, espn_name)
        if name not in fixture_names:
            print(f"  no fixtures match for {espn_name!r} (-> {name!r})", file=sys.stderr)
            continue
        f = form_string(raw.get(tid, []))
        teams.append({"name": name, "form": f})
        if f:
            matched += 1

    teams.sort(key=lambda t: t["name"])
    fixtures["teams"] = teams
    with open(OUT_PATH, "w", encoding="utf-8") as f:
        json.dump(fixtures, f, ensure_ascii=False, indent=2)
        f.write("\n")
    print(f"wrote {len(teams)} teams ({matched} with form) to {OUT_PATH}", file=sys.stderr)


if __name__ == "__main__":
    main()
