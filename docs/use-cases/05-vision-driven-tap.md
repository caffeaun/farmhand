# Vision-driven tap: inspect the screen, find a topic, tap its center

How to use `farmhand inspect` + `farmhand tap` together so a test script can tap "the Login button" or "the cart icon" without anyone hardcoding pixel coordinates.

**Related docs**: [CLI reference](../cli.md) | [Android tap-stress use case](04-android-tap-stress.md)

---

## Why two commands instead of one

The pipeline is deliberately **explicit**:

1. `farmhand inspect` takes a screenshot and asks the vision LLM for the full list of UI topics.
2. The script picks the relevant topic by name (or color/type/text) and computes the bounding box center.
3. `farmhand tap` performs the action.

No composite `vision-tap` exists. The shape lets you:

- Run `inspect` once and reuse the same topic list for several taps on the same screen.
- Log every topic the model saw — useful when the script later picks the wrong one.
- Swap the vision provider, or skip the LLM entirely on iterations that don't need it.

---

## End-to-end script

Prerequisites:
- `farmhand` binary on PATH on a host with `adb` access to the target device.
- `MINIMAX_API_KEY` set in the environment (or whatever you named in `farmhand.yaml`'s `vision.api_key_env`). **Or** a canned topic list and the `--mock-from` flag — see below.
- `jq` for parsing the JSON output.

```bash
#!/usr/bin/env bash
set -euo pipefail

DEVICE="${FARMHAND_DEVICE_ID:-R58W2193TXP}"
NAME_PATTERN="${1:-Login button}"

# 1. Inspect the current screen.
inspection="$(farmhand inspect --device "$DEVICE")"
echo "inspect:" "$inspection"

# 2. Pick the first topic whose name matches NAME_PATTERN (case-insensitive,
#    substring match). Combine name / text fields so phrases like "Sign In"
#    can be matched on either field.
match="$(echo "$inspection" | jq --arg p "$NAME_PATTERN" '
  .topics
  | map(select(((.name // "") + " " + (.text // "")) | ascii_downcase | contains($p | ascii_downcase)))
  | .[0]
')"

if [ "$match" = "null" ] || [ -z "$match" ]; then
  echo "no topic matched $NAME_PATTERN" >&2
  echo "topics returned:" >&2
  echo "$inspection" | jq -r '.topics[] | "  - " + .name' >&2
  exit 1
fi

x1=$(echo "$match" | jq -r '.coordinates.x1')
y1=$(echo "$match" | jq -r '.coordinates.y1')
x2=$(echo "$match" | jq -r '.coordinates.x2')
y2=$(echo "$match" | jq -r '.coordinates.y2')
w=$(echo "$inspection" | jq -r '.screenshot_size.width')
h=$(echo "$inspection" | jq -r '.screenshot_size.height')

# 3. Compute center; clamp into the screen.
cx=$(( (x1 + x2) / 2 ))
cy=$(( (y1 + y2) / 2 ))
if [ "$cx" -lt 0 ] || [ "$cy" -lt 0 ] || [ "$cx" -ge "$w" ] || [ "$cy" -ge "$h" ]; then
  echo "computed tap point ($cx, $cy) outside screen ${w}x${h}; refusing" >&2
  exit 2
fi

echo "tapping ($cx, $cy) — center of $(echo "$match" | jq -r '.name')"
farmhand tap --device "$DEVICE" --x "$cx" --y "$cy"
```

Usage:

```bash
./scripts/inspect-and-tap.sh "Login button"
./scripts/inspect-and-tap.sh "Sign In"            # matches text field
./scripts/inspect-and-tap.sh "search"             # substring, case-insensitive
```

---

## End-to-end testing without spending LLM tokens

`farmhand inspect` accepts `--mock-from <file>` to bypass the vision provider entirely. With it:

- The **real device** is screenshotted (the bridge runs as normal).
- The PNG header is decoded so `screenshot_size` is real.
- The topic list comes from your JSON file instead of MiniMax.

This makes it easy to wire up the inspect → match → tap chain on devices-1 before you ever provision an API key. Example canned topic file:

```json
{
  "topics": [
    {
      "name": "Login button",
      "coordinates": { "x1": 200, "y1": 1800, "x2": 880, "y2": 2000 },
      "color": "blue",
      "type": "button",
      "text": "Sign In"
    },
    {
      "name": "Email field",
      "coordinates": { "x1": 60, "y1": 1100, "x2": 1020, "y2": 1250 },
      "type": "input"
    }
  ]
}
```

Save as `/tmp/mock-topics.json` and run:

```bash
farmhand inspect --device R58W2193TXP --mock-from /tmp/mock-topics.json
```

Stdout will return those topics + the real screenshot's dimensions. The `inspect-and-tap.sh` script above works unchanged — pass `--mock-from` through `farmhand inspect` if your script supports an extra arg, or set `INSPECT_EXTRA_ARGS="--mock-from /tmp/mock-topics.json"` and adapt the call. Verify on the device's pointer-location overlay that the tap lands at the center of the mocked bounding box — that confirms the entire screenshot → JSON → tap chain works.

Once the chain is proven, swap in a real `MINIMAX_API_KEY` and drop `--mock-from` to start using live model output.

---

## Verifying coordinate accuracy

Generalist vision LLMs drift on pixel-level UI targeting — both the bounding boxes and the names can be off. To check whether the model is actually pointing at the element you think it is:

1. **On the device**: Settings → System → Developer options → "Pointer location" (or "Show touches"). Either overlay draws a marker at every touch event.
2. Run the script and watch where the dot lands.
3. If the dot lands off, look at the full `inspect` output — often the model named the topic correctly but the bounding box drifted by 20-40 px. The center is usually still a reasonable tap.
4. If the model named the wrong element, the simplest fix is a more specific match pattern (e.g. match on `.text == "Sign In"` rather than substring on name).

Log the full inspection across several runs; once you have a pile of them you can spot patterns (e.g. "the model always confuses the search bar with the omnibox") and adjust your script.

---

## Filtering on type or color

The topic objects have optional `type` and `color` fields. Filter on them when names alone are ambiguous:

```bash
# Only buttons whose name mentions "submit".
match="$(echo "$inspection" | jq '
  .topics
  | map(select(.type == "button" and (.name | ascii_downcase | contains("submit"))))
  | .[0]
')"
```

```bash
# The blue thing near the bottom.
match="$(echo "$inspection" | jq '
  .topics
  | sort_by(.coordinates.y1) | reverse
  | map(select(.color == "blue"))
  | .[0]
')"
```

---

## When the LLM is wrong

The script above bails on two failure modes (no match, out-of-screen). For a third — "model picked plausible coords but the wrong element" — there's no automatic detection short of asserting on the next screen. A common pattern:

1. `inspect` for the screen, pick + tap.
2. `inspect` again on the resulting screen and assert that an expected post-tap topic is present (e.g. after tapping a Login button you expect to see a "Welcome" topic or a "Password" field).
3. If the assertion fails, back out and pick a different topic.

This "act, then re-inspect" loop leans on the model's semantic understanding rather than its pixel precision, which it's much better at.
