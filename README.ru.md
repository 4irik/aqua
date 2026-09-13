# aqua

**[English version → README.md](README.md)**

Консольная утилита для диктовки: записывает один дубль с микрофона, отправляет в
[AquaVoice API](https://aquavoice.com/docs/api), полученный текст кладёт в буфер
обмена и печатает в stdout.

## Требования

- Linux с PulseAudio/PipeWire
- `parecord` (пакет `pulseaudio-utils`) или `ffmpeg` — захват звука
- `wl-copy` (пакет `wl-clipboard`) — буфер обмена, опционально (`--no-clipboard`)
- Go toolchain — только для сборки

## Установка

### Go

```bash
go install github.com/4irik/aqua@latest
```

### Сборка (требуется Go 1.26+)

```bash
git clone https://github.com/4irik/aqua.git
cd aqua
go build
```

## Получение API-ключей

Ключи создаются в дашборде Aqua: войдите на
[aquavoice.com](https://aquavoice.com) → Account → API keys. Для транскрипции
**должен быть активен API-биллинг**.

Типов ключей два; какой нужен — зависит от режима:

| Режим | Тип ключа в дашборде | Scope | Переменная |
|---|---|---|---|
| `aqua` (по умолчанию) | **Avalon transcription** | `transcription` | `AQUAVOICE_AVALON_KEY` |
| `aqua --dictate` | **Aqua data** | `write` | `AQUAVOICE_API_KEY` |

`AQUAVOICE_API_KEY` также работает как fallback для режима транскрипции, если у
ключа есть нужный scope.

```sh
export AQUAVOICE_AVALON_KEY=...
export AQUAVOICE_API_KEY=...
```

## Использование

```sh
aqua                  # начать запись, Enter или Ctrl-C — стоп → расшифровка
aqua --dictate        # то же, но текст отформатирован (применяются словарь,
                      # замены и инструкции из аккаунта)
aqua --language=ru    # явно задать язык (по умолчанию: auto)
aqua --no-clipboard   # только stdout
```

Текст всегда печатается в stdout; при наличии `wl-copy` также копируется в буфер
обмена. Пустой дубль (остановлен до того, как микрофон дал звук) завершается
тихо с кодом 0.

> **Нюанс:** если источник по умолчанию — Bluetooth-гарнитура, она просыпается
> ~2 с. Дубль короче этого вернёт пустое аудио — либо диктуйте чуть дольше, либо
> смените источник по умолчанию (`pactl set-default-source`).

## Флаги

| Флаг | По умолчанию | Назначение |
|---|---|---|
| `--dictate` | выкл. | Форматированная диктовка через `/dictations` вместо сырой транскрипции |
| `--language` | `auto` | Код языка, передаётся в API как есть |
| `--model` | `avalon-v1.5` | Модель Avalon (режим транскрипции) |
| `--no-clipboard` | выкл. | Только stdout |

## Как это работает

1. `parecord` (или `ffmpeg -f pulse`) пишет запись во временный WAV-файл.
2. Stop-сигнал (Enter/SIGINT) корректно завершает WAV.
3. Файл уходит `multipart/form-data` на выбранный endpoint.
4. При `504 request_timeout` идёт polling `job_id` на
   `/audio/transcription-jobs/{id}/result` до готовности результата.
5. `text` из ответа → stdout + буфер обмена.

## Чего здесь нет (out of scope)

Непрерывный режим с детектором тишины, push-to-talk через демон горячих клавиш,
постобработка текста, конфиг-файл, история дублей.

Детали проекта: [SPEC.md](SPEC.md).
