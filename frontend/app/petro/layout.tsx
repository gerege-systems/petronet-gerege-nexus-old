"use client";

/**
 * The fuel network's five screens, under one heading.
 *
 * The order is the chain's own: what crossed the border, where it was stored,
 * where it is sold — then what is told to the state, and what the state does
 * with it.
 *
 * Tabs carry all five; the sidebar carries three. The three operational screens
 * are one job seen at three points and share a menu entry, because three
 * entries would be three sets of seven locale labels for one task. Reporting
 * and oversight are separate entries because they are separate people: the
 * clerk who sends the daily figures never opens the depot screen, and the
 * official who supervises belongs to a different organisation entirely.
 */

import Link from "next/link";
import { usePathname } from "next/navigation";
import { Fuel } from "lucide-react";

import { useI18n } from "@/lib/i18n";
import { useAccess } from "@/lib/permissions";

export default function FuelLayout({ children }: { children: React.ReactNode }) {
  const { t } = useI18n();
  const pathname = usePathname();
  // Presentation only — every /oversight endpoint checks the permission itself.
  const oversight = useAccess("petro.oversight");

  const tabs = [
    { href: "/petro/shipments", label: t("petro.tab.shipments") },
    { href: "/petro/depots", label: t("petro.tab.depots") },
    { href: "/petro", label: t("petro.tab.stations") },
    { href: "/petro/report", label: t("petro.tab.report") },
    ...(!oversight.loading && oversight.allowed
      ? [{ href: "/petro/oversight", label: t("petro.tab.oversight") }]
      : []),
  ];

  return (
    <div className="p-6 max-w-6xl">
      <header className="mb-6">
        <h1 className="flex items-center gap-3 text-2xl font-semibold text-slate-900">
          <Fuel className="h-6 w-6 text-[var(--gerege-blue)]" />
          {t("petro.view.title")}
        </h1>
      </header>

      <nav className="mb-8 flex gap-1 border-b border-slate-200">
        {tabs.map((tab) => {
          const active = pathname === tab.href;
          return (
            <Link
              key={tab.href}
              href={tab.href}
              className={`-mb-px border-b-2 px-4 py-2 text-sm font-medium transition-colors ${
                active
                  ? "border-[var(--gerege-blue)] text-[var(--gerege-blue)]"
                  : "border-transparent text-slate-500 hover:text-slate-900"
              }`}
            >
              {tab.label}
            </Link>
          );
        })}
      </nav>

      {children}
    </div>
  );
}
