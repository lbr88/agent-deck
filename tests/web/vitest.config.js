import { defineConfig } from 'vitest/config'
import { resolve } from 'node:path'
import { createRequire } from 'node:module'

const repoRoot = resolve(import.meta.dirname, '..', '..')

// Resolve npm packages from the tests/web/node_modules tree so the alias
// values are absolute paths. Bare specifiers used by component sources
// (which live outside tests/web/) wouldn't otherwise find this node_modules.
const req = createRequire(import.meta.url)
const aliasFor = (spec) => req.resolve(spec)

export default defineConfig({
  // Vite root stays at tests/web/ so node_modules resolution works for
  // bare specifiers (preact, htm/preact, @preact/signals). The component
  // sources live one directory up; fs.allow is widened to the repo root.
  root: import.meta.dirname,
  server: {
    fs: {
      allow: [repoRoot],
    },
  },
  test: {
    globals: true,
    environment: 'jsdom',
    setupFiles: ['./helpers/setup.js'],
    include: ['unit/**/*.test.js'],
    coverage: {
      provider: 'v8',
      reporter: ['text', 'html', 'lcov'],
      reportsDirectory: './coverage',
      include: [resolve(repoRoot, 'internal/web/static/app/**/*.js')],
      exclude: [
        resolve(repoRoot, 'internal/web/static/app/main.js'),
      ],
    },
  },
  resolve: {
    // Bare specifiers used by component sources need to resolve to the
    // tests/web/node_modules tree because the components live outside
    // tests/web/ and Vite's bare-specifier resolver walks UP from the
    // file's location — finding nothing at repoRoot/node_modules.
    //
    // Aliasing each spec to its `require.resolve()` result lets Vite jump
    // straight to the installed file. Sub-imports inside those files
    // (e.g. signals.module.js → preact/hooks) re-resolve via the same
    // alias map, so transitive resolution works without breakage.
    // Exact-match regexes prevent the base `preact` alias from rewriting
    // `preact/hooks` into the impossible `preact.js/hooks` path when tests
    // import real components from outside tests/web.
    alias: [
      { find: /^preact\/hooks$/, replacement: aliasFor('preact/hooks') },
      { find: /^preact\/jsx-runtime$/, replacement: aliasFor('preact/jsx-runtime') },
      { find: /^htm\/preact$/, replacement: aliasFor('htm/preact') },
      { find: /^@preact\/signals$/, replacement: aliasFor('@preact/signals') },
      { find: /^@preact\/signals-core$/, replacement: aliasFor('@preact/signals-core') },
      { find: /^preact$/, replacement: aliasFor('preact') },
    ],
  },
})
