#!/bin/bash

OUTPUT_FILE="full_project.txt"

# -----------------------------------------------------------------
# ⚙️ НАСТРОЙКИ ФИЛЬТРАЦИИ
# -----------------------------------------------------------------
# 1. ИСКЛЮЧАЕМЫЕ ПАПКИ И СКРЫТЫЕ ПУТИ (маски путей)
EXCLUDE_DIRS=(
  "*/.*"
  "./vendor/*"
)

# 2. ИСКЛЮЧАЕМЫЕ ФАЙЛЫ (шаблоны / сгенерированный код)
EXCLUDE_FILES=(
  "*.pb.go"          # Сгенерированный Protobuf код
  "*_grpc.pb.go"     # Сгенерированный gRPC код
  # "*_easyjson.go"
  # "go.sum"
)

# 3. ВКЛЮЧАЕМЫЕ ФАЙЛЫ (расширения / точные имена)
INCLUDE_FILES=(
  \(
    -name "*.go"
    -o -name "go.mod"
    -o -name "go.sum"
    -o -name "*.proto"
    # -o -name "*.sql"
  \)
)
# -----------------------------------------------------------------

# 1. Формируем единый чистый массив исключений для find
EXCLUDE_CONDITIONS=()

# Добавляем папки (-path)
for dir in "${EXCLUDE_DIRS[@]}"; do
  if [ ${#EXCLUDE_CONDITIONS[@]} -gt 0 ]; then
    EXCLUDE_CONDITIONS+=(-o)
  fi
  EXCLUDE_CONDITIONS+=(-path "$dir")
done

# Добавляем файлы (-name / -iwholename)
for file_pattern in "${EXCLUDE_FILES[@]}"; do
  if [ ${#EXCLUDE_CONDITIONS[@]} -gt 0 ]; then
    EXCLUDE_CONDITIONS+=(-o)
  fi
  EXCLUDE_CONDITIONS+=(-name "$file_pattern")
done

# 2. Находим файлы с гарантированным отсечением исключений
TMP_LIST=$(mktemp)
find . -type f ! \( "${EXCLUDE_CONDITIONS[@]}" \) -and "${INCLUDE_FILES[@]}" | sort > "$TMP_LIST"

# 3. Интерактивное редактирование списка
echo "📝 Открываем список файлов  в редакторе NANO..."
echo "Удалите строки с файлами, которые НЕ нужно включать, затем сохраните и закройте редактор."
read -p "Нажмите [ENTER]..."

${EDITOR:-nano} "$TMP_LIST"

if [ ! -s "$TMP_LIST" ]; then
  echo "❌ Список файлов пуст. Сборка отменена."
  rm -f "$TMP_LIST"
  exit 1
fi

# 4. Собираем итоговый файл
> "$OUTPUT_FILE"

cat << 'EOF' >> "$OUTPUT_FILE"
=====================
СПИСОК ФАЙЛОВ ПРОЕКТА
=====================
EOF
cat "$TMP_LIST" >> "$OUTPUT_FILE"
echo -e "\n" >> "$OUTPUT_FILE"

cat << 'EOF' >> "$OUTPUT_FILE"
=====================
СОДЕРЖИМОЕ ФАЙЛОВ
=====================
EOF

file_count=0
while read -r file; do
  [ -z "$file" ] && continue

  if [ -f "$file" ]; then
    echo -e "\n--- START OF FILE: $file ---" >> "$OUTPUT_FILE"
    cat "$file" >> "$OUTPUT_FILE"
    echo -e "\n--- END OF FILE: $file ---" >> "$OUTPUT_FILE"
    ((file_count++))
  fi
done < "$TMP_LIST"

# 5. Статистика
total_lines=$(wc -l < "$OUTPUT_FILE")
total_chars=$(wc -m < "$OUTPUT_FILE")

rm -f "$TMP_LIST"

echo ""
echo "✅ Проект успешно собран в $OUTPUT_FILE"
echo "---------------------------------------"
echo "📊 Статистика отчета:"
echo "   - Добавлено файлов: $file_count"
echo "   - Всего строк:      $total_lines"
echo "   - Всего символов:   $total_chars"
echo "---------------------------------------"