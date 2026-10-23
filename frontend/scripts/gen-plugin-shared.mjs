import { readFileSync, writeFileSync, mkdirSync } from 'fs'
import { join, dirname } from 'path'
import { fileURLToPath, pathToFileURL } from 'url'

const __filename = fileURLToPath(import.meta.url)
const __dirname = dirname(__filename)
const frontendRoot = join(__dirname, '..')
const projectRoot = join(__dirname, '..', '..')

// Valid JS identifier pattern: must start with letter, $, or _; can contain alphanumeric, $, _
const validIdentifierPattern = /^[a-zA-Z_$][a-zA-Z0-9_$]*$/

// Reserved words that should not be exported
const reservedWords = new Set([
  'abstract', 'arguments', 'await', 'boolean', 'break', 'byte', 'case', 'catch', 'char', 'class',
  'const', 'continue', 'debugger', 'default', 'delete', 'do', 'double', 'else', 'enum', 'eval',
  'export', 'extends', 'false', 'final', 'finally', 'float', 'for', 'function', 'goto', 'if',
  'implements', 'import', 'in', 'instanceof', 'int', 'interface', 'let', 'long', 'native', 'new',
  'null', 'package', 'private', 'protected', 'public', 'return', 'short', 'static', 'super',
  'switch', 'synchronized', 'this', 'throw', 'throws', 'transient', 'true', 'try', 'typeof',
  'var', 'void', 'volatile', 'while', 'with', 'yield',
])

function isValidExportName(name) {
  if (!validIdentifierPattern.test(name)) return false
  if (reservedWords.has(name)) return false
  if (name === 'default' || name === '__esModule') return false
  return true
}

async function getModuleExports(specifier) {
  // A failed import must fail the build: an empty shim would only surface as
  // a broken plugin at runtime.
  const mod = await import(specifier)
  return Object.keys(mod).filter(isValidExportName).sort()
}

function generateModuleShim(specifier, exportNames, filename) {
  const named = exportNames.length > 0 ? `export const { ${exportNames.join(', ')} } = m;\n` : ''
  return `const m = window.__AINSEL_SHARED__[${JSON.stringify(specifier)}];\nexport default m;\n${named}`
}

export async function generate(outDir = join(frontendRoot, 'public', 'plugin-shared')) {
  // Ensure output directory exists
  mkdirSync(outDir, { recursive: true })

  // Process React modules
  const reactModules = [
    { specifier: 'react', filename: 'react.js' },
    { specifier: 'react-dom', filename: 'react-dom.js' },
    { specifier: 'react-dom/client', filename: 'react-dom-client.js' },
    { specifier: 'react/jsx-runtime', filename: 'react-jsx-runtime.js' },
  ]

  for (const { specifier, filename } of reactModules) {
    const exportNames = await getModuleExports(specifier)
    const code = generateModuleShim(specifier, exportNames, filename)
    writeFileSync(join(outDir, filename), code)
  }

  // Process plugin SDK
  const exportsJsonPath = join(projectRoot, 'packages', 'plugin-sdk', 'exports.json')
  const sdkExports = JSON.parse(readFileSync(exportsJsonPath, 'utf-8'))
  const sdkCode = generateModuleShim('@ainsel/plugin-sdk', sdkExports, 'ainsel-plugin-sdk.js')
  writeFileSync(join(outDir, 'ainsel-plugin-sdk.js'), sdkCode)
}

// Run generator when executed directly
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  generate().catch(error => {
    console.error('Generator failed:', error)
    process.exit(1)
  })
}
