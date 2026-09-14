"use client";

/**
 * The state's screen: what the country holds, who did not say, and where the
 * numbers stop adding up.
 *
 * # Coverage is the first row and that is a finding, not a layout preference
 *
 * Ghana ran the most complete downstream monitoring stack on the continent and
 * its 2025 audit found the software working and the coverage fallen. A stock
 * figure computed from sixty per cent of forecourts is not a stock figure with
 * some uncertainty — it is a different number, quietly. So the share of the
 * country that answered sits above the litres it answered with, and the litres
 * are not shown without it.
 *
 * # The unbuilt balances are shown, marked
 *
 * Three of the five reconciliation balances need a data source that does not
 * exist yet. They are listed anyway, greyed, each saying what it waits for. A
 * ladder that showed only the rungs already built would let everybody forget
 * the other three were ever meant to exist.
 *
 * # Approving is an act, so it asks
 *
 * Returning a report requires a reason in the same keystroke, because "returned
 * — no reason given" costs the sender a day of guessing.
 */

import { useCallback, useEffect, useState } from "react";
import {
  AlertTriangle,
  CheckCircle2,
  Loader2,
  RefreshCw,
  ScrollText,
  Undo2,
} from "lucide-react";

import {
  api,
  type BalanceRow,
  type CensusSummary,
  type NationalRow,
  type ReportGap,
  type ReportSubmission,
} from "@/lib/api";
import { useI18n } from "@/lib/i18n";
import { ErrorNote, GhostButton, PrimaryButton, inputClass, litres } from "@/components/petro/operatorUI";

type Dashboard = {
  day: string;
  coverage: { sites_total: number; sites_reported: number; percent: number };
  products: NationalRow[];
};

export default function OversightPage() {
  const { t } = useI18n();

  const [dashboard, setDashboard] = useState<Dashboard | null>(null);
  const [queue, setQueue] = useState<ReportSubmission[] | null>(null);
  const [gaps, setGaps] = useState<ReportGap[] | null>(null);
  const [balances, setBalances] = useState<BalanceRow[] | null>(null);
  const [census, setCensus] = useState<CensusSummary | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [forbidden, setForbidden] = useState(false);
  const [returning, setReturning] = useState<string | null>(null);
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState<string | null>(null);

  const load = useCallback(() => {
    const fail = (err: unknown) => {
      const message = err instanceof Error ? err.message : String(err);
      // A tenant that is not a supervisory body is not an error to debug; it is
      // a person who followed a menu entry meant for somebody else. Both locks
      // answer 403 — the platform's petro.oversight permission in English, the
      // module's supervisory-body check in Mongolian — so the status decides,
      // not the wording. Matching only the Mongolian left every section
      // spinning under a raw English error.
      if ((err as { status?: number }).status === 403) {
        setForbidden(true);
        return;
      }
      setError(message);
    };

    api.fuelNationalDashboard().then(setDashboard).catch(fail);
    api.fuelReviewQueue().then((r) => setQueue(r.submissions)).catch(fail);
    api.fuelReportGaps().then((r) => setGaps(r.missing)).catch(fail);
    api.fuelReconciliation().then((r) => setBalances(r.balances)).catch(fail);
    api.fuelCensusSummary().then(setCensus).catch(() => setCensus(null));
  }, []);

  useEffect(load, [load]);

  const decide = useCallback(
    async (id: string, approve: boolean, note: string) => {
      setBusy(id);
      try {
        if (approve) {
          await api.approveFuelReport(id);
        } else {
          await api.returnFuelReport(id, note);
        }
        setReturning(null);
        setReason("");
        load();
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err));
      } finally {
        setBusy(null);
      }
    },
    [load],
  );

  const recompute = useCallback(async () => {
    setBusy("refresh");
    try {
      await api.refreshFuelDaily();
      load();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(null);
    }
  }, [load]);

  if (forbidden) {
    return (
      <p className="rounded-2xl border border-slate-200 bg-white p-10 text-center text-sm text-slate-500">
        {t("petro.oversight.forbidden")}
      </p>
    );
  }

  return (
    <div className="space-y-8">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <p className="max-w-2xl text-slate-500">{t("petro.oversight.subtitle")}</p>
        <GhostButton onClick={recompute} disabled={busy === "refresh"}>
          <span className="inline-flex items-center gap-2">
            <RefreshCw className={`h-4 w-4 ${busy === "refresh" ? "animate-spin" : ""}`} />
            {t("petro.oversight.refresh")}
          </span>
        </GhostButton>
      </div>

      {error ? <ErrorNote>{error}</ErrorNote> : null}

      {/* Coverage, then stock. Never the other way round. */}
      <section>
        {dashboard === null ? (
          <Loader2 className="h-5 w-5 animate-spin text-slate-400" />
        ) : (
          <>
            <div className="rounded-2xl border border-slate-200 bg-white p-6">
              <div className="flex flex-wrap items-baseline justify-between gap-4">
                <div>
                  <span className="text-xs font-medium uppercase tracking-wide text-slate-400">
                    {t("petro.oversight.coverage")} · {dashboard.day}
                  </span>
                  <p className="mt-1 text-3xl font-semibold tabular-nums text-slate-900">
                    {dashboard.coverage.percent.toFixed(1)}%
                  </p>
                </div>
                <p className="text-sm text-slate-500">
                  {dashboard.coverage.sites_reported} / {dashboard.coverage.sites_total}{" "}
                  {t("petro.oversight.reported_of")}
                </p>
              </div>
              <div className="mt-4 h-2 w-full overflow-hidden rounded-full bg-slate-100">
                <div
                  className={`h-full rounded-full ${
                    dashboard.coverage.percent < 50
                      ? "bg-red-500"
                      : dashboard.coverage.percent < 80
                        ? "bg-amber-500"
                        : "bg-emerald-500"
                  }`}
                  style={{ width: `${Math.min(100, dashboard.coverage.percent)}%` }}
                />
              </div>
            </div>

            <div className="mt-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
              {dashboard.products.map((product) => (
                <div
                  key={product.product_code}
                  className="rounded-2xl border border-slate-200 bg-white p-5"
                >
                  <span className="text-sm font-medium text-slate-900">
                    {product.product_label}
                  </span>
                  <p className="mt-2 text-2xl font-semibold tabular-nums text-slate-900">
                    {litres(product.stock_liters)}{" "}
                    <span className="text-sm font-normal text-slate-400">л</span>
                  </p>
                  <p className="mt-1 text-xs text-slate-500">
                    {product.days_of_supply != null
                      ? `${product.days_of_supply.toFixed(1)} ${t("petro.oversight.days")}`
                      : "—"}
                  </p>
                  <p className="mt-2 text-xs text-slate-400">
                    {product.sites_reported} / {product.sites_total}
                  </p>
                </div>
              ))}
            </div>
          </>
        )}
      </section>

      {/* The queue: one official, two hundred companies. */}
      <section>
        <h2 className="mb-3 text-sm font-semibold text-slate-900">
          {t("petro.oversight.queue")}
        </h2>
        {queue === null ? (
          <Loader2 className="h-5 w-5 animate-spin text-slate-400" />
        ) : queue.length === 0 ? (
          <p className="rounded-2xl border border-slate-200 bg-white p-8 text-center text-sm text-slate-500">
            {t("petro.oversight.queue_empty")}
          </p>
        ) : (
          <div className="overflow-x-auto rounded-2xl border border-slate-200 bg-white">
            <table className="w-full text-sm">
              <thead className="border-b border-slate-200 text-left text-xs text-slate-500">
                <tr>
                  <th className="px-4 py-3 font-medium">Байгууллага</th>
                  <th className="px-4 py-3 font-medium">{t("petro.report.periods")}</th>
                  <th className="px-4 py-3 text-right font-medium">Мөр</th>
                  <th className="px-4 py-3 font-medium">&nbsp;</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-100">
                {queue.map((submission) => (
                  <tr key={submission.id} className="align-top">
                    <td className="px-4 py-3">
                      <span className="font-medium text-slate-900">
                        {submission.tenant_name}
                      </span>
                      <span className="block text-xs text-slate-400">
                        {t("petro.report.version")} {submission.version} · {submission.source}
                      </span>
                    </td>
                    <td className="px-4 py-3 text-slate-600">{submission.period_start}</td>
                    <td className="px-4 py-3 text-right tabular-nums">
                      {submission.row_count}
                      {submission.warning_count > 0 ? (
                        <span className="ml-2 inline-flex items-center gap-1 text-xs text-amber-600">
                          <AlertTriangle className="h-3 w-3" />
                          {submission.warning_count}
                        </span>
                      ) : null}
                    </td>
                    <td className="px-4 py-3">
                      {returning === submission.id ? (
                        <div className="flex flex-wrap items-center gap-2">
                          <input
                            autoFocus
                            className={`${inputClass} w-64`}
                            placeholder={t("petro.oversight.return_reason")}
                            value={reason}
                            onChange={(event) => setReason(event.target.value)}
                          />
                          <PrimaryButton
                            busy={busy === submission.id}
                            disabled={reason.trim() === ""}
                            onClick={() => decide(submission.id, false, reason)}
                          >
                            {t("petro.oversight.return")}
                          </PrimaryButton>
                          <GhostButton onClick={() => setReturning(null)}>×</GhostButton>
                        </div>
                      ) : (
                        <div className="flex justify-end gap-2">
                          <GhostButton onClick={() => setReturning(submission.id)}>
                            <span className="inline-flex items-center gap-1">
                              <Undo2 className="h-4 w-4" />
                              {t("petro.oversight.return")}
                            </span>
                          </GhostButton>
                          <PrimaryButton
                            busy={busy === submission.id}
                            onClick={() => decide(submission.id, true, "")}
                          >
                            <CheckCircle2 className="h-4 w-4" />
                            {t("petro.oversight.approve")}
                          </PrimaryButton>
                        </div>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      {/* Who did not report — the cheapest thing this system does. */}
      <section>
        <h2 className="mb-3 text-sm font-semibold text-slate-900">
          {t("petro.oversight.gaps")}
        </h2>
        {gaps === null ? (
          <Loader2 className="h-5 w-5 animate-spin text-slate-400" />
        ) : gaps.length === 0 ? (
          <p className="rounded-2xl border border-emerald-200 bg-emerald-50 p-6 text-center text-sm text-emerald-700">
            {t("petro.oversight.gaps_empty")}
          </p>
        ) : (
          <div className="overflow-hidden rounded-2xl border border-slate-200 bg-white">
            <ul className="divide-y divide-slate-100">
              {gaps.map((gap) => (
                <li key={gap.tenant_id} className="flex items-center justify-between px-4 py-3">
                  <span className="text-sm font-medium text-slate-900">{gap.tenant_name}</span>
                  <span className="text-xs text-slate-400">
                    {gap.sites_total} · {t("petro.oversight.last_seen")}:{" "}
                    {gap.last_reported_at?.slice(0, 10) ?? "—"}
                  </span>
                </li>
              ))}
            </ul>
          </div>
        )}
      </section>

      {/* The five balances. */}
      <section>
        <h2 className="mb-3 text-sm font-semibold text-slate-900">
          {t("petro.oversight.reconciliation")}
        </h2>
        {balances === null ? (
          <Loader2 className="h-5 w-5 animate-spin text-slate-400" />
        ) : (
          <div className="overflow-hidden rounded-2xl border border-slate-200 bg-white">
            <ul className="divide-y divide-slate-100">
              {balances.map((balance) => (
                <li
                  key={balance.code}
                  className={`flex flex-wrap items-center justify-between gap-3 px-4 py-3 ${
                    balance.available ? "" : "opacity-50"
                  }`}
                >
                  <div>
                    <span className="font-mono text-sm font-medium text-slate-900">
                      {balance.code}
                    </span>
                    <span className="ml-3 text-sm text-slate-600">{balance.name}</span>
                  </div>
                  {balance.available ? (
                    <div className="flex items-center gap-4 text-sm tabular-nums">
                      <span
                        className={
                          balance.delta_pct != null &&
                          Math.abs(balance.delta_pct) > balance.tolerance_pct
                            ? "font-medium text-red-600"
                            : "text-slate-700"
                        }
                      >
                        {balance.delta_pct != null ? `${balance.delta_pct.toFixed(2)}%` : "—"}
                      </span>
                      <span className="text-xs text-slate-400">
                        {t("petro.oversight.tolerance")} {balance.tolerance_pct}%
                      </span>
                      {balance.breaches > 0 ? (
                        <span className="text-xs text-amber-600">
                          {balance.breaches} {t("petro.oversight.breaches")}
                        </span>
                      ) : null}
                    </div>
                  ) : (
                    <span className="text-xs text-slate-400">
                      {t("petro.oversight.waiting_for")}: {balance.waiting}
                    </span>
                  )}
                </li>
              ))}
            </ul>
          </div>
        )}
      </section>

      {/* The census: what February's driver order will be decided from. */}
      {census ? (
        <section>
          <h2 className="mb-3 flex items-center gap-2 text-sm font-semibold text-slate-900">
            <ScrollText className="h-4 w-4 text-slate-400" />
            {t("petro.oversight.census")}
            <span className="font-normal text-slate-400">
              {census.surveyed} / {census.stations_total} {t("petro.oversight.surveyed")}
            </span>
          </h2>
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-5">
            {census.classes.map((row) => (
              <div
                key={row.class || "none"}
                className="rounded-2xl border border-slate-200 bg-white p-4"
              >
                <span className="text-xs text-slate-400">{row.class || "—"}</span>
                <p className="text-xl font-semibold tabular-nums text-slate-900">
                  {row.stations}
                </p>
                <p className="mt-1 text-xs text-slate-500">{row.label}</p>
              </div>
            ))}
          </div>
        </section>
      ) : null}
    </div>
  );
}
