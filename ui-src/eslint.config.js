import js from '@eslint/js'
import globals from 'globals'
import pluginVue from 'eslint-plugin-vue'
import tseslint from 'typescript-eslint'

// Фаза 0 рерайта (44-дока). Legacy пока вне линта: panel.js - vanilla JS
// со своей дисциплиной (node --check), LegacyPanel.vue удаляется в Фазе 3.
export default tseslint.config(
  { ignores: ['node_modules/**', 'dist/**', 'public/**', 'src/views/**'] },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  ...pluginVue.configs['flat/recommended'],
  {
    files: ['**/*.vue'],
    languageOptions: { parserOptions: { parser: tseslint.parser } },
  },
  {
    // UiIcon вставляет статические SVG-константы (icons.ts, без
    // пользовательского ввода); html-комментарии в шаблоне там нельзя -
    // превращают компонент во фрагмент и рвут fallthrough-атрибуты.
    files: ['src/components/ui/UiIcon.vue'],
    rules: { 'vue/no-v-html': 'off' },
  },
  {
    files: ['scripts/**/*.mjs', '*.config.js'],
    languageOptions: { globals: { ...globals.node } },
  },
  {
    languageOptions: { globals: { ...globals.browser } },
    rules: {
      '@typescript-eslint/no-unused-vars': ['error', { argsIgnorePattern: '^_' }],
    },
  },
)
