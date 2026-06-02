# Android tap-stress test job

How to run a tap-stress test against the FarmHand Android fleet without the job runner ever calling `adb` directly.

**Related docs**: [CLI reference](../cli.md) | [API reference](../API_REFERENCES.md)

---

## Principle

The architecture rule is: **only FarmHand's ADB bridge talks to `adb`.** A job's `test_command` therefore must not shell out to `adb shell input tap` — instead it calls `farmhand tap`, which dispatches the tap through the same bridge the server uses for device discovery and lifecycle commands.

This means:

- The job's `test_command` is `adb`-free; the runner image only needs the `farmhand` binary.
- The bridge keeps its single-writer view of the device, so a tap loop in one job can't race with a wake/reboot from another.
- The tap rate and coordinates live in the job spec, not in scripts. Reading old runs back is straightforward.

---

## Minimal stress-tap job

Job YAML / API payload:

```json
{
  "test_command": "scripts/tap-loop.sh",
  "device_filter": { "platform": "android" },
  "timeout_minutes": 35,
  "env": {
    "TAP_X": "540",
    "TAP_Y": "960",
    "TAP_RATE_PER_MIN": "246",
    "TAP_DURATION_SECONDS": "1800"
  }
}
```

`scripts/tap-loop.sh` (checked in alongside the job):

```bash
#!/usr/bin/env bash
set -euo pipefail

: "${FARMHAND_DEVICE_ID:?FARMHAND_DEVICE_ID must be set by the executor}"
: "${TAP_X:?missing}"
: "${TAP_Y:?missing}"
: "${TAP_RATE_PER_MIN:?missing}"
: "${TAP_DURATION_SECONDS:?missing}"

interval_ms=$(( 60000 / TAP_RATE_PER_MIN ))
sleep_seconds=$(awk -v ms="$interval_ms" 'BEGIN { printf "%.3f", ms/1000 }')
end_epoch=$(( $(date +%s) + TAP_DURATION_SECONDS ))

taps=0
echo "starting tap loop on $FARMHAND_DEVICE_ID — rate ${TAP_RATE_PER_MIN}/min for ${TAP_DURATION_SECONDS}s"

while [ "$(date +%s)" -lt "$end_epoch" ]; do
  farmhand tap --device "$FARMHAND_DEVICE_ID" --x "$TAP_X" --y "$TAP_Y"
  taps=$(( taps + 1 ))
  sleep "$sleep_seconds"
done

echo "finished — total taps: $taps"
```

The executor injects `FARMHAND_DEVICE_ID` for the script (and a few other `FARMHAND_*` variables — see [Executor docs](../../README.md)). The script never calls `adb`; every tap flows through the bridge.

---

## Verifying the bridge actually drove the device

`farmhand tap` exits 0 when the adb command succeeded, which proves the bridge issued `input tap`. It does **not** prove the tap landed on the right UI element. For visual verification:

1. Install a coordinates-overlay app on the device that draws a dot at every touch event (any of the developer-options "show touches" toggles works as a quick check).
2. Run the job at a slow rate (`TAP_RATE_PER_MIN=30`) and eyeball where the dots land.
3. Once the coordinates check out, raise the rate.

If you suspect a tap drifted off-target (e.g. the screen rotated or the layout changed), capture a screenshot artifact — see the [job-artifacts docs](../../README.md) for the screenshot capture flag (rolling out in Phase B of the device-IO work).

---

## Running outside a job

The same script works from any shell that has `farmhand` on PATH and a config pointing at the right database — useful for one-off debugging:

```bash
FARMHAND_DEVICE_ID=R58W2193TXP \
TAP_X=540 TAP_Y=960 \
TAP_RATE_PER_MIN=60 \
TAP_DURATION_SECONDS=30 \
./scripts/tap-loop.sh
```

If `farmhand` errors with `device R58W2193TXP is offline`, run a fresh discovery cycle (`farmhand devices`) and check the cable / wireless connection before re-running.
