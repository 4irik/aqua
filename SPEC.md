# aqua — спецификация

CLI-утилита: записывает один дубль с микрофона, отправляет в AquaVoice API,
полученный текст кладёт в буфер обмена и печатает в stdout.

Целевая платформа: Linux/Wayland. Один бинарник на Go, внешних Go-зависимостей нет.

## CLI

```
aqua [--raw] [--language CODE] [--model ID] [--no-clipboard] [--verbose]
```

| Флаг | Значение по умолчанию | Назначение |
|---|---|---|
| `--raw` | выкл. | Сырая транскрипция Avalon вместо диктовки (нужен ключ со scope `transcription`) |
| `--language` | `auto` | Язык распознавания, передаётся в API как есть |
| `--model` | `avalon-v1.5` | Модель Avalon (только `--raw`) |
| `--no-clipboard` | выкл. | Не трогать буфер обмена, только stdout |
| `--verbose` | выкл. | После остановки — длительность дубля и размер WAV; во время ожидания API — спиннер на stderr |

## Режимы и endpoints

| Режим | Endpoint | Scope ключа | Ответ |
|---|---|---|---|
| Диктовка (default) | `POST https://api.aquavoice.com/v1/dictations` | `write` | `{ "text": "...", "session_id": "...", "raw_text": "..." }` — берём `text` |
| Транскрипция (`--raw`) | `POST https://api.aquavoice.com/v1/audio/transcriptions` | `transcription` | `{ "text": "..." }` |

Оба вызова — `multipart/form-data`, авторизация `Authorization: Bearer <key>`.

- Транскрипция: поля `file` (аудиофайл), `model`, `language`.
- Диктовка: поля `audio` (аудиофайл), `operation=dictate`, `language`.

## Ключи

- `AQUAVOICE_API_KEY` — ключ типа «Aqua data» (scope `write`), для режима по
  умолчанию.
- `AQUAVOICE_AVALON_KEY` — ключ типа «Avalon transcription» (scope
  `transcription`), для `--raw`. Если не задана, используется
  `AQUAVOICE_API_KEY`.

Отсутствие нужного ключа — ошибка на старте, до начала записи: сообщение называет
переменную и требуемый scope, exit code 1.

## Поток работы

1. Проверить ключ (см. выше) и наличие рекордера. Ошибка — сразу на stderr, exit 1.
2. Запустить запись: `parecord <tmp.wav>` во временный файл. На stderr
   печатать индикатор (`Recording… Enter/Ctrl-C to stop`).
3. Stop-сигнал — Enter или SIGINT. При остановке: послать SIGINT процессу
   рекордера (он корректно допишет WAV-заголовок), дождаться выхода, прочитать
   файл.
4. Отправить записанный WAV multipart-запросом на endpoint текущего режима.
5. Распарсить `text` из ответа. Напечатать в stdout. При `--verbose` и наличии
   `session_id` в ответе — напечатать его на stderr. Если не `--no-clipboard` —
   скопировать через `wl-copy`.

Режим диктовки сохраняет каждый дубль сессией в истории аккаунта Aqua
(отключается privacy mode в настройках аккаунта).

`parecord` отсутствует → `ffmpeg -f pulse -i default -y <tmp.wav>`; ни одного
нет → ошибка с подсказкой.

## Краевые случаи

- **504 на синхронном endpoint'е**: тело ошибки несёт `error.job_id` — опрашивать
  `GET /v1/audio/transcription-jobs/{job_id}/result` (409 = ещё не готово, retry)
  до результата. Пользователю на stderr — `Still processing…`.
- **Не-2xx без job_id**: тело ошибки на stderr как есть, exit 1.
- **`wl-copy` не найден**: warning на stderr, текст всё равно в stdout.
- **Пустая запись** (stop сразу после старта): не отправлять запрос, exit 0.

## Проект

```
go.mod
main.go                    # вся утилита
main_test.go               # httptest-мок: оба режима + 504-polling
scripts/dump-server.py     # echo-дампер запросов; целиться через AQUA_BASE_URL
```

Только stdlib: `flag`, `os/exec`, `net/http`, `mime/multipart`, `encoding/json`,
`os/signal`.

## Out of scope

- Непрерывный режим с детектором тишины (VAD).
- Push-to-talk через внешний демон горячих клавиш.
- Постобработка текста, история дублей, конфиг-файл.
