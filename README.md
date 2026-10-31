# ccbm

**Claude Code better manager.** A [btop](https://github.com/aristocratos/btop)-style terminal dashboard for your Claude Code usage. It shows how much of your 5-hour and weekly limits you've used, how long until they reset, what you're working on right now, and what every past session cost in tokens.

![ccbm wide layout](docs/screenshot-wide.png)

<p align="center"><img src="docs/screenshot-narrow.png" width="420" alt="ccbm narrow / phone layout"></p>

## Panels

| # | Box | What it shows |
|---|-----|---------------|
| ¹ | **5h window** | Braille graph of token burn across the current 5-hour window. The usage panel shows % used, time until reset, tokens (input / output / cache), API-equivalent cost, burn rate, a **pace** forecast ("⚠ limit ~20:07" or "✓ safe until reset"), and a per-project breakdown. |
| ² | **weekly** | Weekly limit meters (all models, plus per-model limits when your plan has them), the reset time, and per-day token bars for the last 7 days. |
| ³ | **now** | Sessions active in the last 15 min: project, git branch, model, your latest prompt, the last tool Claude ran, and session totals. |
| ⁴ | **history** | Process-list style table of every session: start time, duration, project, task title, model, tokens, and cost. The selected row expands into a detail pane with the full token breakdown and model mix. |

The layout adapts to the terminal size. At 100+ columns you get the btop grid. Narrower terminals (phones over SSH, tmux splits) get a stacked layout.

## Install

```sh
go install github.com/heyjeay/ccbm@latest
```

Or build from source with `go build -o ccbm .`

## Usage

```sh
ccbm              # live dashboard
ccbm --demo       # explore with generated sample data
ccbm --offline    # don't contact Anthropic; estimate limits locally
ccbm --snapshot --width 120 --height 40   # print one frame and exit
```

| Key | Action |
|-----|--------|
| `↑` `↓` / `j` `k` | select a history row |
| `PgUp` `PgDn` `g` `G` | jump through the history |
| `s` / `Tab` | cycle sort: recent → tokens → cost → duration |
| `+` / `-` | change refresh interval |
| `r` | refresh now |
| `q` / `Ctrl-C` | quit |

## Where the numbers come from

- **Tokens, sessions, prompts, and tools** are read from Claude Code's local transcripts (`~/.claude/projects/**/*.jsonl`, or `$CLAUDE_CONFIG_DIR`). Files are tailed incrementally, so a refresh only reads newly appended bytes. Streamed responses that Claude Code logs more than once are de-duplicated by message and request ID.
- **5h and weekly utilization (%) and reset times** come from the same endpoint Claude Code's `/usage` screen uses (`api.anthropic.com/api/oauth/usage`). ccbm authenticates with your existing Claude Code login, read from `~/.claude/.credentials.json` or the macOS keychain. The token is only ever sent to `api.anthropic.com`. ccbm polls about once a minute and shows `● live` in the top-right corner.
- **Offline / estimated mode** (`--offline`, or when no login is found): the 5h window is reconstructed ccusage-style (it starts at the top of the hour of your first message), and % used is estimated against your most expensive previous window. The weekly box becomes a rolling 7-day view. The corner shows `● local est.`
- **Cost** is the API-equivalent price at published per-token rates, including cache-write (5m / 1h) and cache-read pricing. It's a yardstick for how heavy a session was, not what your subscription bills you.

## Development

```sh
go test ./...
go run . --demo
```

The renderer draws onto a cell canvas (`internal/ui/canvas.go`) and emits truecolor escapes, the same way btop does. The colours come from btop's default theme (`internal/ui/theme.go`).
