/**
 * @brief useZodForm — opinionated wrapper around `react-hook-form`'s `useForm`
 *        that wires the `@hookform/resolvers/zod` resolver automatically and
 *        exposes a generic `setApiErrors` helper for surfacing server-side
 *        validation failures back onto fields.
 *
 * The hook stays intentionally minimal:
 *   - it does NOT import an i18n function. Consumers receive Zod's raw
 *     `message` strings (typically i18n keys placed in the schema, e.g.
 *     `z.string().min(1, 'profile.validation.display_name_required')`) and
 *     call `t()` themselves at the render site.
 *   - it does NOT attempt to parse a particular API error envelope. The
 *     caller hands `setApiErrors` an iterable of `{field, message, type?}`
 *     records — usually mapped from a `ProblemJson` / `ApiError` object — and
 *     the hook forwards them to `react-hook-form`'s `setError`.
 *
 * `react-hook-form` and `zod` are declared as optional peer dependencies of
 * `@nodate-flow/ui` so consumers that don't use forms aren't forced to pull
 * them in. When importing this hook the two peers MUST be installed in the
 * consuming app.
 *
 * @example
 * ```tsx
 * const profileSchema = z.object({
 *   displayName: z.string().min(1, 'profile.validation.display_name_required'),
 * });
 *
 * const { register, handleSubmit, formState: { errors, isSubmitting }, setApiErrors }
 *   = useZodForm(profileSchema, { displayName: '' });
 *
 * const onSubmit = handleSubmit(async (values) => {
 *   const { error } = await sdk.PATCH('/me', { body: values });
 *   if (error?.fieldErrors) {
 *     setApiErrors(error.fieldErrors); // [{ field: 'displayName', message: '...' }, ...]
 *   }
 * });
 * ```
 */

import { zodResolver } from '@hookform/resolvers/zod';
import { useCallback } from 'react';
import {
  type DefaultValues,
  type FieldValues,
  type Path,
  type Resolver,
  type UseFormProps,
  type UseFormReturn,
  useForm,
} from 'react-hook-form';
import type { z } from 'zod';

/**
 * @brief A single server-side validation error pinned to a form field.
 *
 * `field` MUST match a key path in the form values. `message` is whatever
 * the consumer wants to display (typically an i18n key resolved later, or
 * an already-translated string). `type` defaults to `'server'`.
 */
export interface ApiFieldError<TValues extends FieldValues = FieldValues> {
  field: Path<TValues>;
  message: string;
  type?: string;
}

/**
 * @brief Options accepted by `useZodForm`.
 *
 * Mirrors a useful subset of `UseFormProps`: `defaultValues` is the
 * positional second argument for ergonomics, and any other RHF option can
 * be passed via this object. The `resolver` field is intentionally omitted
 * — the hook always supplies `zodResolver(schema)`.
 */
export type UseZodFormOptions<
  TInput extends FieldValues,
  TOutput extends FieldValues = TInput,
> = Omit<UseFormProps<TInput, unknown, TOutput>, 'resolver' | 'defaultValues'>;

/**
 * @brief Return shape of `useZodForm`. Adds `setApiErrors` to the standard
 *        `UseFormReturn`.
 *
 * `TInput` is the schema's input type (the raw field values held by the form
 * controls) and `TOutput` is its output type (the values produced after any
 * Zod transforms run on a successful submit). For schemas without transforms
 * the two coincide.
 */
export interface UseZodFormReturn<TInput extends FieldValues, TOutput extends FieldValues = TInput>
  extends UseFormReturn<TInput, unknown, TOutput> {
  /**
   * @brief Map an iterable of server-side field errors back onto the form.
   * Errors with an unknown `field` are silently ignored so callers can pass
   * raw API responses without filtering.
   */
  setApiErrors: (errors: Iterable<ApiFieldError<TInput>>) => void;
}

/**
 * @brief Wrap `useForm` with a Zod resolver and a `setApiErrors` helper.
 *
 * @param schema    Zod schema describing the form values.
 * @param defaultValues Initial values; required because controlled inputs
 *                  must always have a defined initial value.
 * @param options   Any other `UseFormProps` to forward (e.g. `mode`).
 *
 * The form's field values are typed as the schema's input (`z.input`); the
 * submit handler receives the schema's output (`z.output`). With
 * `@hookform/resolvers` v5 the Zod resolver is a Standard Schema adapter, so
 * the schema is constrained to `z.ZodType` and the input/output types are
 * read off it rather than from a `parse()` return type.
 */
export function useZodForm<TInput extends FieldValues, TOutput extends FieldValues = TInput>(
  schema: z.ZodType<TOutput, TInput>,
  defaultValues: DefaultValues<TInput>,
  options?: UseZodFormOptions<TInput, TOutput>,
): UseZodFormReturn<TInput, TOutput> {
  const form = useForm<TInput, unknown, TOutput>({
    ...options,
    resolver: zodResolver(schema) as Resolver<TInput, unknown, TOutput>,
    defaultValues,
  });

  const setApiErrors = useCallback(
    (errors: Iterable<ApiFieldError<TInput>>) => {
      for (const e of errors) {
        form.setError(
          e.field,
          { type: e.type ?? 'server', message: e.message },
          { shouldFocus: false },
        );
      }
    },
    [form],
  );

  return Object.assign(form, { setApiErrors });
}
