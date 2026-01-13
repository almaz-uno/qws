# Font Fallback Implementation

## Overview

Реализована поддержка множественных fallback шрифтов на уровне глифов (символов) для `qws`.

## Архитектура

### MultiFallbackFace

Новый тип `MultiFallbackFace` в [pkg/carousel/fontfallback.go](pkg/carousel/fontfallback.go) реализует интерфейс `font.Face` из `golang.org/x/image/font` и обеспечивает:

1. **Glyph-level fallback** — автоматический подбор шрифта для каждого символа
2. **Множественные fallback** — поддержка цепочки из нескольких шрифтов
3. **Pure Go** — работает без CGo, используя только `golang.org/x/image` и `golang/freetype`

### Принцип работы

```
Текст: "Hello 世界 🎨"
  ↓
'H' → проверяем в primary font → найден → используем primary
'世' → проверяем в primary font → не найден → проверяем в fallback #1 → найден
'🎨' → primary → fallback #1 → fallback #2 → используем последний
```

### Метрики шрифтов

Метрики (высота строки, базовая линия) берутся из **основного (primary) шрифта** для согласованности отображения.

Кернинг применяется только если оба символа из одного шрифта.

## Конфигурация

```yaml
appearance:
  font:
    paths:
      - "/usr/share/fonts/truetype/noto/NotoSans-Regular.ttf"
      - "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"
      - "/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf"
    size: 14
```

### Командная строка

```bash
qws --appearance-font-paths=/path/to/font1.ttf,/path/to/font2.ttf
```

## Использование

Класс автоматически используется в [pkg/carousel/renderer.go](pkg/carousel/renderer.go) при рендеринге текста:

```go
fallbackFace := NewMultiFallbackFace(cfg.FontPaths, fontSize)
if fallbackFace == nil {
    // Skip rendering if no fonts available
    return
}
defer fallbackFace.Close()
dc.SetFontFace(fallbackFace)
dc.DrawString(text, x, y)
```

## Тестирование

Тесты находятся в [pkg/carousel/fontfallback_test.go](pkg/carousel/fontfallback_test.go):

```bash
go test ./pkg/carousel/...
```

## Ограничения

1. **Не fontconfig** — требуются полные пути к файлам шрифтов, системные имена типа "Noto Sans" не поддерживаются (можно добавить через `fc-match`)
2. **Производительность** — для каждого глифа проверяется несколько шрифтов, но это быстро для небольших текстов
3. **Сложные языки** — нет поддержки лигатур и сложной обработки (арабский, деванагари и т.д.)

## Будущие улучшения

- [ ] Кеширование результатов поиска глифов
- [ ] Поддержка системных имён шрифтов через `fc-match`
- [ ] Автоматическое определение шрифтов для CJK/Emoji
- [ ] Метрики на основе используемого шрифта (не только primary)
