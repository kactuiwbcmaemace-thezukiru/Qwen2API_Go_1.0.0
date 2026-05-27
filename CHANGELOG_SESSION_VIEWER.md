# Changelog: Admin Cached Chat Viewer

## Реализовано в этом патче

Добавлена отдельная вкладка **Cached Chats** во встроенную React/Next админку. Она появляется рядом с Overview, Accounts, Settings, Debug и другими разделами и доступна в том же интерфейсе, который открывается через `127.0.0.1:3000/v1/admin`.

## Что изменилось

### Backend

- `internal/storage/conversations.go`
  - `ConversationSession` расширен полями `created_at`, `last_message`, `message_count`, `has_tools`, `tools_used`, `messages`.
  - Добавлен компактный тип `CachedChatMessage` для отображения сообщений в админке.
  - Существующие режимы хранения `memory`, `file`, `redis` продолжают использовать тот же `ConversationStore`.

- `internal/openai/conversation_sessions.go`
  - Добавлено кеширование snapshot переписки через `CacheExchange(...)`.
  - Кеш сохраняет последние OpenAI-style сообщения запроса и финальный ответ assistant.
  - Максимальный размер snapshot ограничен `maxCachedConversationMessages = 100`.
  - Для первого single-turn чата без context hash создаётся synthetic cache key на основе `chat_id`, чтобы такой чат тоже появлялся в админке.
  - `Save(...)` теперь не затирает уже сохранённые сообщения, когда обновляется только metadata mapping.
  - Добавлена публичная очистка устаревших сессий `CleanupExpired()`.

- `internal/openai/chat_execution.go`
  - `executedChat` теперь содержит `chat_type`, `context_hash`, `account_email`, `chat_id`, чтобы после ответа можно было записать кеш для админки.

- `internal/openai/handler.go`
  - После успешного `/v1/chat/completions` сохраняется кеш переписки.
  - Поддержаны both streaming и non-streaming ответы.
  - Для tool calls сохраняется нормализованный OpenAI-compatible payload, который можно раскрыть в viewer.

- `internal/admin/handler.go`
  - `GET /api/sessions` теперь возвращает расширенный список сессий.
  - `GET /api/sessions/chat?context_hash=...` возвращает конкретную сессию и кешированные сообщения.
  - Также можно искать чат через `chat_id`: `GET /api/sessions/chat?chat_id=...`.
  - `DELETE /api/sessions` удаляет выбранную session mapping/cache по `context_hash`.
  - `POST /api/sessions/clear-expired` очищает истёкшие записи.

- `internal/server/server.go`
  - Зарегистрирован новый endpoint `/api/sessions/clear-expired`.

### Frontend

- `public/app/ui/components/sessions-tab.tsx`
  - Новый UI-раздел **Cached Chats**.
  - Таблица сессий: аккаунт, модель, тип чата, количество сообщений, last message, updated time, tools, actions.
  - Панель просмотра сообщений в стиле chat bubbles.
  - Поддержаны роли `user`, `assistant`, `system`, `tool`.
  - `reasoning_content` и `tool_calls` можно раскрывать через `<details>`.
  - Добавлены кнопки Refresh, Clear expired, View, Open in Qwen, Delete session.

- `public/app/ui/admin-dashboard.tsx`
  - Добавлен пункт навигации `Cached Chats`.
  - Добавлен render новой вкладки.

- `public/app/ui/types.ts`
  - Добавлены типы `SessionItem`, `SessionsResponse`, `SessionChatResponse`, `CachedChatMessage`.
  - `TabKey` расширен значением `sessions`.

- `public/app/sessions/page.tsx`
  - Добавлена прямая frontend route-страница, открывающая админку сразу на вкладке `sessions`.

- `public/app/i18n/locales/*.json`
  - Добавлены nav labels для `sessions`.

## API

Фактические пути в текущем проекте идут через `/api/...`, потому что остальные экраны админки уже используют такие relative endpoints.

```bash
# список кешированных чатов
GET /api/sessions

# просмотр сообщений выбранной сессии
GET /api/sessions/chat?context_hash=<hash>

# альтернативно по chat_id
GET /api/sessions/chat?chat_id=<qwen-chat-id>

# удалить одну сессию
DELETE /api/sessions
Content-Type: application/json

{"context_hash":"<hash>"}

# очистить истёкшие сессии
POST /api/sessions/clear-expired
```

## Как пользоваться

1. Запустить проект как обычно.
2. Открыть админку: `http://127.0.0.1:3000/v1/admin`.
3. Вставить admin API key, если админка его запросит.
4. Сделать один или несколько запросов через `/v1/chat/completions`.
5. Открыть вкладку **Cached Chats** и нажать **Refresh**.
6. Нажать **View** у нужной сессии, чтобы увидеть кешированные сообщения.

## Ограничения

- Это именно proxy-side cache, а не полноценная синхронизация всей истории из Qwen.
- Для `DATA_SAVE_MODE=memory` кеш пропадает после перезапуска процесса.
- Для `file` и `redis` кеш хранится вместе с conversation session mapping.
- Истёкшие записи удаляются по существующему TTL `conversationSessionTTL = 24h`.
- Если старые сессии были созданы до этого патча, у них может быть только metadata; сообщения появятся после следующего успешного `/v1/chat/completions`.

## Проверка в sandbox

- `gofmt` применён к изменённым Go-файлам.
- Go syntax parser успешно прошёл по изменённым Go-файлам.
- TS/TSX syntax transpile check успешно прошёл по изменённым frontend-файлам.
- Полный `go test ./...` в sandbox не завершился, потому что окружение не имеет доступа к `proxy.golang.org` и не может скачать отсутствующие модули `github.com/aliyun/aliyun-oss-go-sdk` и `github.com/redis/go-redis/v9`.
- Полная Next/TypeScript сборка в sandbox не запускалась, потому что в архиве нет `public/node_modules`, а установка npm-зависимостей требует сети.

## Дополнительное исправление: продолжение одного Qwen-чата в tool loop

После первого теста в Cline стало видно, что каждый шаг tool loop создавал отдельный upstream Qwen chat. Причина была в том, что mapping сохранялся только для context hash входящего запроса, но следующий OpenAI-compatible запрос уже содержит новый префикс истории: предыдущие `user/assistant/tool` сообщения плюс новый последний `tool` или `user` message.

Исправлено:

- `internal/openai/handler.go`
  - После каждого успешного ответа теперь дополнительно вычисляется continuation context hash для состояния диалога **после** assistant response.
  - Для tool loop используется sentinel tail message, чтобы tool reminder не попадал в hash предыдущего assistant tool call и следующий запрос находил тот же upstream `chat_id`.

- `internal/openai/conversation_sessions.go`
  - Добавлен `CacheExchangeWithAliases(...)`: один и тот же upstream Qwen chat теперь может иметь несколько context-hash aliases.
  - `ListAll()` дедуплицирует aliases по `account_email + chat_id`, чтобы вкладка **Cached Chats** не превращалась в список дублей одного и того же разговора.
  - `Delete(context_hash)` удаляет все aliases выбранного upstream чата, а не только один hash.
  - Добавлен `computeContextHashForPrefix(...)` для явного hashing уже готового префикса истории.

Практический эффект:

- Обычный multi-turn клиент должен продолжать один и тот же upstream Qwen chat.
- Cline/Roo-style tool loop больше не должен создавать новый Qwen chat на каждом `tool_result` шаге.
- В админке один upstream chat должен отображаться одной строкой, а не серией почти одинаковых записей.

Примечание про кнопку **Open in Qwen**:

- Эта кнопка открывает `https://chat.qwen.ai/c/<chat_id>`.
- Она сработает только если сам сайт Qwen всё ещё видит этот chat id и браузер залогинен именно в тот же Qwen-аккаунт, который указан в строке `Account`.
- Даже если proxy-cache показывает сообщения, это не доказывает, что Qwen UI отдаст этот чат в браузере: proxy-cache хранится локально, а Qwen UI проверяет upstream историю и текущую browser-сессию.
