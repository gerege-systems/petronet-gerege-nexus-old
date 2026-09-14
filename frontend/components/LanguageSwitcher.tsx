"use client";

import { LOCALES, useI18n } from "@/lib/i18n";

/**
 * Locale toggle: the language's code, in words.
 *
 * It used to carry a circle flag beside each code. A flag is a country and not
 * a language — English is not the United States and Arabic is not Saudi
 * Arabia, and Mongolian is read on both sides of a border. The code alone says
 * the thing without claiming the other.
 *
 * The visible label stays the short code, so the control is the same size it
 * was. The full name is added out of sight for a screen reader, after the
 * code, so the accessible name still contains what is on screen (WCAG 2.5.3).
 */
export default function LanguageSwitcher({ variant = "light" }: { variant?: "light" | "dark" }) {
  const { locale, setLocale, availableLocales, t } = useI18n();
  // Only the languages this device has switched on — the full LOCALES list is
  // the catalogue, not the offer.
  const offered = LOCALES.filter((option) => availableLocales.includes(option.code));

  const base =
    variant === "dark"
      ? "border-slate-700 bg-slate-900/70"
      : "border-slate-200 bg-white";
  const activeStyle =
    variant === "dark"
      ? "bg-indigo-500/20 text-white"
      : "bg-indigo-50 text-indigo-700";
  const idleStyle =
    variant === "dark"
      ? "text-slate-400 hover:text-slate-200"
      : "text-slate-500 hover:text-slate-800";

  return (
    <div
      className={`inline-flex items-center gap-0.5 rounded-lg border p-0.5 ${base}`}
      role="group"
      aria-label={t("base.field.language")}
    >
      {offered.map((option) => (
        <button
          key={option.code}
          type="button"
          onClick={() => setLocale(option.code)}
          aria-pressed={locale === option.code}
          className={`flex items-center rounded-md px-2 py-1 text-xs font-semibold transition ${
            locale === option.code ? activeStyle : idleStyle
          }`}
        >
          <span className="uppercase">{option.code}</span>
          <span className="sr-only"> {option.label}</span>
        </button>
      ))}
    </div>
  );
}
