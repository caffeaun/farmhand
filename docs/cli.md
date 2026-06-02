# FarmHand CLI reference

The `farmhand` binary is both the server and a thin client. Server commands are documented in the [README](../README.md); this page covers the device-IO subcommands that wrap adb so jobs (and ad-hoc scripts) never have to call adb themselves.

**Related docs**: [API Reference](API_REFERENCES.md) | [Android tap-stress use case](use-cases/04-android-tap-stress.md)

---

## Device input subcommands

These commands talk to FarmHand's ADB bridge directly — no HTTP round-trip — so they must run on the host that has adb access (the same host as the FarmHand server). They open the configured SQLite database read-only to look up the device by ID; they do not write to it.

All four commands share the same exit contract:

- `0` on success.
- non-zero on failure, with a one-line error printed to **stderr**.

Devices are addressed by their FarmHand `id` (USB serial like `R58W2193TXP`, or wireless ip:port like `192.168.1.5:5555`). Non-Android devices return `... not supported for platform <platform>`; offline devices return `device <id> is offline`; missing devices return a not-found error.

### `farmhand tap`

Dispatch one tap at pixel coordinates `(x, y)`.

```
farmhand tap --device <id> --x <int> --y <int>
```

Example:

```
farmhand tap --device R58W2193TXP --x 540 --y 960
```

### `farmhand swipe`

Dispatch a swipe gesture. `--duration-ms` is the gesture duration in milliseconds; `0` (the default) lets the device pick.

```
farmhand swipe --device <id> \
  --from-x <int> --from-y <int> \
  --to-x <int>   --to-y <int> \
  [--duration-ms <int>]
```

Example — scroll up:

```
farmhand swipe --device R58W2193TXP \
  --from-x 540 --from-y 1800 \
  --to-x 540   --to-y 600 \
  --duration-ms 300
```

### `farmhand keyevent`

Dispatch a single keyevent. `--keycode` accepts a non-negative integer (e.g. `4` for BACK) or a symbolic name matching `^KEYCODE_[A-Z0-9_]+$`.

```
farmhand keyevent --device <id> --keycode <KEYCODE_X|int>
```

Examples:

```
farmhand keyevent --device R58W2193TXP --keycode KEYCODE_BACK
farmhand keyevent --device R58W2193TXP --keycode 26     # power
```

The keycode is validated before adb sees it; arbitrary strings (including ones with shell metacharacters) are rejected.

### `farmhand text`

Type text into whatever input field is focused on the device.

```
farmhand text --device <id> --text "<string>"
```

Example:

```
farmhand text --device R58W2193TXP --text "hello world"
```

The text is shell-quoted on the device side before being passed to `input text`, so embedded metacharacters like `;`, `&`, `|`, `$`, backticks, etc. are treated as literal characters and cannot escape into the device shell.

---

## Vision-driven inspection

### `farmhand inspect`

Takes a screenshot of the device, POSTs it to the configured vision LLM (MiniMax-M3 by default) with a system prompt asking for a structured inspection, and prints the resulting topic list as JSON on **stdout**:

```
farmhand inspect --device <id>
```

Stdout shape:

```json
{
  "topics": [
    {
      "name": "Login button",
      "coordinates": { "x1": 100, "y1": 1700, "x2": 980, "y2": 1900 },
      "color": "blue",
      "type": "button",
      "text": "Sign In"
    },
    {
      "name": "Email field",
      "coordinates": { "x1": 60, "y1": 1100, "x2": 1020, "y2": 1250 },
      "type": "input"
    }
  ],
  "screenshot_size": { "width": 1080, "height": 2400 }
}
```

Each topic always has `name` + `coordinates` (an axis-aligned bounding box in screenshot pixel space, origin top-left). `color`, `type`, and `text` are best-effort and may be empty when the model could not infer them. `screenshot_size` is included so callers can sanity-check that bounding boxes are inside the screen before computing tap coordinates.

To tap an element, pipe the inspection through `jq` and compute the box center yourself — see [`docs/use-cases/05-vision-driven-tap.md`](use-cases/05-vision-driven-tap.md) for a complete script.

Configuration lives in the `vision:` block of `farmhand.yaml`; the API key is read at invocation time from the env var named by `api_key_env` (default `MINIMAX_API_KEY`). An empty key disables the command with a clear error.

**Accuracy caveat**: generalist vision LLMs drift on pixel coordinates — research shows ~20px error on a 1080×2400 screen is normal, and bounding boxes can be slightly looser or tighter than the true element. Enabling Developer options → "Show pointer location" on the device lets you eyeball where the inspection actually points before relying on it for taps.

---

## Why a CLI instead of REST?

The runner that submits a job runs on the same host as the FarmHand bridge today, so adding REST endpoints for tap/swipe/keyevent/text would just add HTTP round-trips for no gain. The CLI talks straight to the bridge in-process. If a future deployment needs remote input dispatch, REST endpoints can be layered on top of the same ADBBridge methods without changing the CLI surface.

The architecture rule still holds: **only FarmHand's bridge talks to `adb`.** Jobs call `farmhand tap`, not `adb shell input tap`.
