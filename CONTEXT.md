# Glossary

- **Take (дубль)** — одна сессия записи: от запуска `aqua` до Enter/Ctrl-C. Одна запись = один запрос к API.
- **Transcription mode** — режим по умолчанию: сырая расшифровка через `/v1/audio/transcriptions` (Avalon, scope `transcription`).
- **Dictation mode** — `--dictate`: форматированный текст через `/v1/dictations` (scope `write`), применяет настройки аккаунта.
- **Stop-сигнал** — Enter или SIGINT во время записи; завершает дубль и запускает отправку.
