<div align="center">

# AmPulsar

**A stream notification bot that treats a chat message as state, not as an event.**

[![CI](https://github.com/am-kenny/ampulsar/actions/workflows/ci.yml/badge.svg)](https://github.com/am-kenny/ampulsar/actions/workflows/ci.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/am-kenny/ampulsar?logo=go&logoColor=white)](go.mod)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

</div>

---

AmPulsar posts one message when a channel goes live, pins it if you ask. Once the broadcast is over, the bot unpins the Live message and either posts a new message with a recording or edits the existing one, depending on the policy you choose. The live session is persisted, so bot restarting mid-stream continues where it paused.

Observes a **Twitch** or **TikTok** channel and posts updates to **Telegram**.

## Quick start

### Telegram
<!-- TODO: Create a required permissions tables for Telegram channels and supergroups -->
You need a Telegram bot token from [BotFather](https://t.me/BotFather) and ID of the chat to post into (can be retrieved with [ID BOT](https://t.me/idbot)). Add the bot to that chat, granting it required permissions.

### Twitch
Twitch source requires an application to be registered in the [Twitch developer console](https://dev.twitch.tv/console/apps). Client ID and client secret are both provided by Twitch. 

### TikTok
TikTok source requires only the username to be set.

### Run it
```bash
git clone https://github.com/am-kenny/ampulsar.git
cd ampulsar
# put your configuration in .env, see tables below
docker compose up -d
```

The `docker-compose.yaml` file mounts a named volume at `/app/data` to store the session.

To run it without Docker, build with Go 1.27 or newer:

```bash
go build -o ampulsar ./cmd/bot
```

The binary then writes a session file to the XDG state directory, `~/.local/state/ampulsar/session.json` on Linux. Set `STORE_PATH` in order to override the default path.

## Configuration

Configuration is made with environment variables grouped by platform. A group with no environment variables set is considered off.

### Source, pick one

| Variable | Required | Description |
| --- | --- | --- |
| `TWITCH_CLIENT_ID` | with Twitch | Client id of your Twitch application |
| `TWITCH_CLIENT_SECRET` | with Twitch | Client secret, used for an app access token |
| `TWITCH_CHANNEL_NAME` | with Twitch | Channel login name to watch, lowercase, no URL |
| `TIKTOK_USERNAME` | with TikTok | Username to watch, without the `@` |

Twitch source is currently prioritized over TikTok.

### Telegram

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `TELEGRAM_BOT_TOKEN` | yes | | Bot token from [BotFather](https://t.me/BotFather) |
| `TELEGRAM_CHAT_ID` | yes | | Target chat ID from [ID BOT](https://t.me/idbot) |
| `TELEGRAM_PIN` | no | `false` | Pin while live, unpin at the end |
| `TELEGRAM_ACTION_ON_END` | no | `edit_in_place` | `edit_in_place`, `new_message`, `delete` or `none` |

`edit_in_place` rewrites live message with offline template, `new_message` posts new message with offline template, `delete` deletes live message, `none` does nothing (except unpinning, if the message was pinned). The first two policies wait for the stream recording. If recording is not published within `POLL_END_GRACE` the policy falls back to `none` and closes the session. TikTok does not have stream recordings published, you should use `delete` or `none` with TikTok source.

### Messages and runtime

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `TEMPLATE_STYLE` | no | `default` | `default` or `simplified` |
| `TEMPLATE_LANGUAGE` | no | `ru` | `eng` or `ru` |
| `POLL_INTERVAL` | no | `1m` | Interval between each poll, for example `15s`, `2m30s` |
| `POLL_END_GRACE` | no | `10m` | Wait period for a recording after a stream ends |
| `STORE_PATH` | no | XDG state directory | Session file location. Defaults to `/app/data/store.json` in Docker image |

## Messages

Templates are Go templates currently embedded in the binary. They are distinguished by kind, style and language. The default template renders:

> 🔴 Now live: Space Age - day 3
>
> ▶️ **AmKenny | Factorio**

and, once the recording is up:

> ⚫ Stream ended: Space Age - day 3 (2:42:35)
>
> 📼 **Watch the recording**

Titles are HTML escaped, so characters like `<` and `&` cannot break the message. The `simplified` style currently has only live templates, you should set `TELEGRAM_ACTION_ON_END` to `none` or `delete`.

## Roadmap

- [x] **Twitch source**
- [x] **Telegram destination**
- [x] **TikTok source**
- [x] **File persistence**, restart safe mid-stream
- [ ] **Discord destination** (partially done)
- [ ] **Several destinations at once**
- [ ] **YouTube source**
- [ ] **Postgres persistence**
- [ ] **Many channels** + batch polling
- [ ] **HTTP API**
- [ ] **Auth**
- [ ] **Web UI**

## License

MIT © Andrii Prykhodko. See [LICENSE](LICENSE).
