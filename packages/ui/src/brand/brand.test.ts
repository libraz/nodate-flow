import { describe, expect, it } from 'vitest';
import { brandFileStem, DEFAULT_APP_NAME, isDataUri, resolveBrand } from './brand';

const name = (v: string) => resolveBrand({ VITE_APP_NAME: v }).appName;

describe('resolveBrand name', () => {
  it('defaults when unset, empty or blank', () => {
    expect(resolveBrand({})).toEqual({
      appName: DEFAULT_APP_NAME,
      logo: undefined,
      icon: undefined,
    });
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

const HTTPS_DATA = /must be an https URL or a data:image\/\(png\|svg\+xml\|webp\|jpeg\|gif\) URI/;
const ASCII = /must contain only printable ASCII characters/;
const HEAD = 'data:image/svg+xml,';
const DATA_MAX = HEAD + 'a'.repeat(32_768 - HEAD.length);

const kinds = [
  ['LOGO', 'VITE_APP_LOGO_URL', 'VITE_APP_LOGO_DARK_URL', 'logo'],
  ['ICON', 'VITE_APP_ICON_URL', 'VITE_APP_ICON_DARK_URL', 'icon'],
] as const;

describe.each(kinds)('resolveBrand %s', (_kind, base, dark, field) => {
  const get = (v: string) => resolveBrand({ [base]: v })[field];

  it.each(['', '   ', '\t\n'])('is unset for %j', (v) => {
    expect(get(v)).toBeUndefined();
  });

  it('is unset when the variable is absent', () => {
    expect(resolveBrand({})[field]).toBeUndefined();
  });

  it('trims ASCII whitespace only', () => {
    expect(get(' https://cdn.example/l.svg ')?.src).toBe('https://cdn.example/l.svg');
    expect(() => get('\u00A0https://cdn.example/l.svg')).toThrow(ASCII);
  });

  it('accepts an upper-case https scheme as is', () => {
    expect(get('HTTPS://cdn.example/l.svg')?.src).toBe('HTTPS://cdn.example/l.svg');
  });

  it('accepts data URIs case-insensitively', () => {
    expect(get('DATA:image/png;base64,AAAA')?.src).toBe('DATA:image/png;base64,AAAA');
    expect(get('data:image/svg+xml,%3Csvg%3E')?.src).toBe('data:image/svg+xml,%3Csvg%3E');
  });

  it.each(['png', 'webp', 'jpeg', 'gif'])('accepts data:image/%s;base64', (t) => {
    expect(get(`data:image/${t};base64,AAAA`)?.src).toBe(`data:image/${t};base64,AAAA`);
  });

  it.each([
    'data:image/bmp;base64,AAAA',
    'data:text/html,<script>1</script>',
    'http://cdn.example/l.svg',
    'https://',
    'cdn.example/l.svg',
    '/logo.svg',
    'javascript:alert(1)',
  ])('rejects %s with the https/data error', (v) => {
    expect(() => get(v)).toThrow(HTTPS_DATA);
    expect(() => get(v)).toThrow(new RegExp(base));
  });

  it.each(['https://cdn.example/a b.svg', 'https://a.example/x\ny', 'https://例え.jp/l.svg'])(
    'rejects %j as non printable ASCII',
    (v) => {
      expect(() => get(v)).toThrow(ASCII);
      expect(() => get(v)).toThrow(new RegExp(base));
    },
  );

  it('accepts a 32768 character data URI and rejects 32769', () => {
    expect(DATA_MAX).toHaveLength(32_768);
    expect(get(DATA_MAX)?.src).toBe(DATA_MAX);
    expect(() => get(`${DATA_MAX}a`)).toThrow(`${base} data URI must be at most 32768 characters`);
  });

  it.each(['', '   '])('requires the base when only DARK is set (base %j)', (b) => {
    expect(() => resolveBrand({ [base]: b, [dark]: 'https://cdn.example/d.svg' })).toThrow(
      `${dark} requires ${base}`,
    );
  });

  it('treats a blank DARK as unset', () => {
    expect(resolveBrand({ [base]: 'https://cdn.example/l.svg', [dark]: '   ' })[field]).toEqual({
      src: 'https://cdn.example/l.svg',
      darkSrc: undefined,
    });
  });

  it('resolves the dark source and validates it', () => {
    expect(
      resolveBrand({ [base]: 'https://cdn.example/l.svg', [dark]: 'https://cdn.example/d.svg' })[
        field
      ],
    ).toEqual({ src: 'https://cdn.example/l.svg', darkSrc: 'https://cdn.example/d.svg' });
    expect(() =>
      resolveBrand({ [base]: 'https://cdn.example/l.svg', [dark]: 'http://cdn.example/d.svg' }),
    ).toThrow(new RegExp(dark));
  });
});

describe('isDataUri', () => {
  it('detects data: case-insensitively', () => {
    expect(isDataUri('data:image/png;base64,AA')).toBe(true);
    expect(isDataUri('DATA:x')).toBe(true);
    expect(isDataUri('https://x.example')).toBe(false);
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
