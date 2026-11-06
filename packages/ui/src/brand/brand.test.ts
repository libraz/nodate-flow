import { describe, expect, it } from 'vitest';
import { brandFileStem, DEFAULT_APP_NAME, resolveBrand } from './brand';

const name = (v: string) => resolveBrand({ VITE_APP_NAME: v }).appName;
const logo = (v: string) => resolveBrand({ VITE_APP_LOGO_URL: v }).logoUrl;

describe('resolveBrand name', () => {
  it('defaults when unset, empty or blank', () => {
    expect(resolveBrand({})).toEqual({ appName: DEFAULT_APP_NAME, logoUrl: undefined });
    expect(name('')).toBe(DEFAULT_APP_NAME);
    expect(name('   ')).toBe(DEFAULT_APP_NAME);
  });

  it('trims surrounding whitespace', () => {
    expect(name('  Acme  ')).toBe('Acme');
  });

  it('counts code points: 64 passes (astral included), 65 fails', () => {
    expect(name('a'.repeat(63) + '😀')).toHaveLength(65);
    expect(() => name('a'.repeat(65))).toThrow(/VITE_APP_NAME/);
    expect(name('😀'.repeat(64))).toBe('😀'.repeat(64));
    expect(() => name('😀'.repeat(65))).toThrow(/VITE_APP_NAME/);
  });

  it.each([
    ['newline', 'a\nb'],
    ['NUL', 'a\u0000b'],
    ['DEL', 'a\u007Fb'],
    ['C1', 'a\u0085b'],
    ['U+2028', 'a b'],
    ['U+2029', 'a b'],
    ['bidi override', 'a‮b'],
    ['bidi isolate', 'a⁦b'],
    ['colon', 'a:b'],
  ])('rejects %s', (_label, value) => {
    expect(() => name(value)).toThrow(/VITE_APP_NAME/);
  });

  it('does not echo the value in the error', () => {
    expect(() => name('secret:value')).toThrow(/^(?!.*secret)/);
  });
});

describe('resolveBrand logo', () => {
  it('is undefined when unset or blank', () => {
    expect(logo('')).toBeUndefined();
    expect(logo('  ')).toBeUndefined();
  });

  it('accepts https and trims', () => {
    expect(logo(' https://cdn.example.com/logo.svg ')).toBe('https://cdn.example.com/logo.svg');
  });

  it('accepts allowed data URIs up to 32768 characters', () => {
    const head = 'data:image/svg+xml,';
    const ok = head + 'a'.repeat(32_768 - head.length);
    expect(ok).toHaveLength(32_768);
    expect(logo(ok)).toBe(ok);
    expect(() => logo(`${ok}a`)).toThrow(/VITE_APP_LOGO_URL/);
  });

  it.each(['png', 'webp', 'jpeg', 'gif'])('accepts data:image/%s;base64', (t) => {
    expect(logo(`data:image/${t};base64,AAAA`)).toBe(`data:image/${t};base64,AAAA`);
  });

  it.each([
    ['http', 'http://cdn.example.com/logo.svg'],
    ['javascript', 'javascript:alert(1)'],
    ['relative', '/logo.svg'],
    ['unparsable', 'not a url'],
    ['html data URI', 'data:text/html,<script>1</script>'],
  ])('rejects %s', (_label, value) => {
    expect(() => logo(value)).toThrow(/VITE_APP_LOGO_URL/);
  });
});

describe('brandFileStem', () => {
  it('replaces unsafe characters and collapses whitespace', () => {
    expect(brandFileStem('Acme Corp')).toBe('Acme-Corp');
    expect(brandFileStem('a/b\\c*d?e"f<g>h|i')).toBe('a-b-c-d-e-f-g-h-i');
    expect(brandFileStem('a  \t b')).toBe('a-b');
  });

  it('falls back to the default when nothing remains', () => {
    expect(brandFileStem('///')).toBe(DEFAULT_APP_NAME);
    expect(brandFileStem('')).toBe(DEFAULT_APP_NAME);
  });
});
