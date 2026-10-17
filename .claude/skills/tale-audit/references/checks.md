# Проверки ревизии: команды

Все команды из корня репозитория. Результаты попадают в отчёт ревизии таблицами.

## Инвентаризация

```bash
# строки и символы каждого файла системы промптов
for f in CLAUDE.md .claude/rules/*.md .claude/skills/*/SKILL.md .claude/skills/*/references/*.md; do
  [ -f "$f" ] && printf "%5d строк %6d симв. %s\n" "$(wc -l < "$f")" "$(wc -m < "$f")" "$f"
done

# объём «всегда в контексте»: точка входа + главный скилл
cat CLAUDE.md .claude/skills/tale-team/SKILL.md | wc -m

# дата последней правки каждого файла
for f in CLAUDE.md .claude/rules/*.md .claude/skills/*/SKILL.md; do
  printf "%s  %s\n" "$(git log -1 --format=%ad --date=short -- "$f")" "$f"
done | sort

# файлы без правок дольше трёх месяцев
for f in .claude/rules/*.md .claude/skills/*/SKILL.md; do
  d=$(git log -1 --format=%ad --date=short -- "$f")
  [ "$d" \< "$(date -d '-3 months' +%F)" ] && echo "$d  $f"
done
```

## Целостность ссылок

```bash
# пути со слешем в обратных кавычках, которых нет ни от корня, ни от .claude/, docs/, internal/
# (эвристика: имена вида vN.md и шаблоны проверять глазами)
grep -ohE '`[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)+`' \
  CLAUDE.md .claude/rules/*.md .claude/skills/*/SKILL.md .claude/skills/*/references/*.md 2>/dev/null \
  | tr -d '`' | sort -u | while read -r p; do
    for r in "" .claude/ docs/ internal/; do [ -e "$r$p" ] && continue 2; done; echo "нет: $p"; done

# упомянутые цели make, которых нет в Makefile
grep -ohE 'make [a-z][a-z-]*' CLAUDE.md .claude/rules/*.md .claude/skills/*/SKILL.md | sort -u \
  | while read -r _ t; do grep -qE "^$t:" Makefile || echo "нет цели: make $t"; done

# упомянутые ADR, которых нет
grep -ohE 'ADR-[0-9]{4}' .claude/rules/*.md .claude/skills/*/SKILL.md | sort -u \
  | while read -r a; do ls docs/decisions/${a#ADR-}-*.md >/dev/null 2>&1 || echo "нет: $a"; done

# paths правил: каждый префикс до первого * должен существовать
grep -h '^  - "' .claude/rules/*.md | tr -d ' "-' | sed 's/\*.*//; s|/$||' | sort -u \
  | while read -r p; do [ -z "$p" ] || [ -e "$p" ] || echo "нет каталога: $p"; done
```

## Карта скиллов и правил

```bash
# скиллы и правила на диске против карты главного скилла и docs/README.md
ls .claude/skills | while read -r s; do
  grep -q "$s" .claude/skills/tale-team/SKILL.md || echo "нет в карте tale-team: $s"
  grep -q "$s" docs/README.md || echo "нет в docs/README.md: $s"
done
ls .claude/rules | while read -r r; do
  grep -q "rules/$r" .claude/skills/tale-team/SKILL.md || echo "нет в карте tale-team: rules/$r"
done
# обратное: записи карты, которых нет на диске
grep -oE '/tale-[a-z-]+' .claude/skills/tale-team/SKILL.md | tr -d / | sort -u \
  | while read -r s; do [ -d ".claude/skills/$s" ] || echo "в карте, нет на диске: $s"; done

# нижний уровень не ссылается на верхний: правила не упоминают скиллы и CLAUDE.md
grep -nE 'tale-team|/tale-[a-z-]+|CLAUDE\.md' .claude/rules/*.md | grep -v '^.claude/rules/meta.md'
```

## Устаревшие упоминания

```bash
# закрытые пункты плана и бэклога, упомянутые в промптах
grep -ohE '\bB[0-9]{1,3}\b' .claude/rules/*.md .claude/skills/*/SKILL.md | sort -u \
  | while read -r b; do grep -q "| $b " docs/plan/backlog-done.md 2>/dev/null && echo "закрыт: $b"; done
grep -ohE 'пункт[а-я]* [0-9]{1,2}\b' .claude/rules/*.md .claude/skills/*/SKILL.md | sort -u

# даты и хеши коммитов в промптах — кандидаты в «балласт» или «реализация»
grep -nE '20[0-9]{2}-[0-9]{2}-[0-9]{2}|\b[0-9a-f]{7}\b' .claude/rules/*.md .claude/skills/*/SKILL.md

# длинные пункты (> 300 символов) — кандидаты в документ
awk 'length($0) > 300 && /^(- |[0-9]+\. )/ {print FILENAME":"FNR": "length($0)" симв."}' .claude/rules/*.md .claude/skills/*/SKILL.md

# один и тот же тезис в нескольких файлах: повторяющиеся предложения длиннее 60 символов
cat .claude/rules/*.md .claude/skills/*/SKILL.md | tr ';' '\n' | sed 's/^[ -]*//' \
  | awk 'length($0) > 60' | sort | uniq -d
```

## Пересечение описаний скиллов

```bash
# ключевые слова «Подключать при словах …» по скиллам; одинаковые слова у двух скиллов — спорное место
for f in .claude/skills/*/SKILL.md; do
  n=$(basename "$(dirname "$f")")
  grep -oE '«[^»]+»' "$f" | head -40 | sed "s/^/$n /"
done | sort -k2 | uniq -D -f1
```
