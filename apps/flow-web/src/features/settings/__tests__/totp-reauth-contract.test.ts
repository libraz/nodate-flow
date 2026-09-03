/**
 * Enrolling in two-factor authentication re-authenticates, and the
 * generated SDK types are what hold every call site to it: if the
 * password leaves the enroll or confirm body, the requests the settings
 * panel builds must stop compiling rather than start quietly attaching
 * an authenticator app for whoever holds the session.
 *
 * The assertions below are compile-time. `tsc -b` fails if any
 * `@ts-expect-error` stops being an error — that is, if a body without a
 * password becomes constructible — and the route equalities fail if
 * either endpoint stops taking the body that carries the password. A
 * text search over the generated module cannot make that claim: a
 * substring such as `password: string;` matches any schema in the file,
 * so it stays green with the enrollment body empty.
 *
 * That the password actually reaches the wire is a separate, behavioural
 * claim, asserted on the outgoing request in totp-reauth.test.tsx.
 */

import type { components, paths } from '@nodate-flow/sdk';
import { describe, expect, it } from 'vitest';

type EnrollBody = components['schemas']['TotpEnrollInputBody'];
type ConfirmBody = components['schemas']['TotpConfirmInputBody'];

type EnrollRequestBody =
  paths['/me/totp/enroll']['post']['requestBody']['content']['application/json'];
type ConfirmRequestBody =
  paths['/me/totp/confirm']['post']['requestBody']['content']['application/json'];

/**
 * Structural identity rather than assignability in one direction: a body
 * that merely extends the schema would still satisfy `extends`, and an
 * `any` on either side would satisfy it always.
 */
type Identical<A, B> =
  (<T>() => T extends A ? 1 : 2) extends <T>() => T extends B ? 1 : 2 ? true : false;

describe('TOTP enrollment reauth contract', () => {
  it('requires a password on the enrollment body', () => {
    // @ts-expect-error enrollment re-authenticates: the password is required
    const withoutPassword: EnrollBody = {};
    expect(withoutPassword).toBeDefined();

    // @ts-expect-error the password is the account password, not a client-side flag
    const notAPassword: EnrollBody = { password: true };
    expect(notAPassword).toBeDefined();

    const accepted: EnrollBody = { password: 'hunter2' };
    expect(accepted).toEqual({ password: 'hunter2' });
  });

  it('requires both the code and the password on the confirmation body', () => {
    // @ts-expect-error the code proves possession of the authenticator, not of the account
    const codeOnly: ConfirmBody = { code: '123456' };
    expect(codeOnly).toBeDefined();

    // @ts-expect-error the confirmation still has to name the code it is confirming
    const passwordOnly: ConfirmBody = { password: 'hunter2' };
    expect(passwordOnly).toBeDefined();

    const accepted: ConfirmBody = { code: '123456', password: 'hunter2' };
    expect(accepted).toEqual({ code: '123456', password: 'hunter2' });
  });

  it('wires both routes to the bodies that carry the password', () => {
    const enrollTakesEnrollBody: Identical<EnrollRequestBody, EnrollBody> = true;
    const confirmTakesConfirmBody: Identical<ConfirmRequestBody, ConfirmBody> = true;

    expect(enrollTakesEnrollBody).toBe(true);
    expect(confirmTakesConfirmBody).toBe(true);
  });
});
