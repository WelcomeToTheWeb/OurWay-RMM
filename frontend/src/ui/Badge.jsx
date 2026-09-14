// Kit badge family (existing classes):
//   Badge      — red count chip (`.badge`), e.g. open-alert counts
//   StatusPill — status pill (`.pill .pill-<status>`)
//   Score      — anomaly-score chip (`.score .hot/.warm/.cool`), σ display
export function fmtNum(n) {
	if (n === null || n === undefined) return "—";
	return (Math.round(n * 100) / 100).toString();
}

// Badge variants: ok (green), offline (amber), err (red), default (red count chip)
export function Badge({ children, className = "", variant = "" }) {
	let variantClass = "";
	if (variant === "ok") {
		variantClass = "badge-ok";
	} else if (variant === "offline") {
		variantClass = "badge-offline";
	} else if (variant === "err") {
		variantClass = "badge-err";
	}
	return (
		<span className={"badge" + (variantClass ? " " + variantClass : "") + (className ? " " + className : "")}>
			{children}
		</span>
	);
}

export function StatusPill({ status, children }) {
	return <span className={"pill pill-" + status}>{children ?? status}</span>;
}

// σ score → tone: ≥10σ hot, ≥5σ warm, else cool (baseline z-score bands).
export function scoreTone(score) {
	return score >= 10 ? "hot" : score >= 5 ? "warm" : "cool";
}

export function Score({ score }) {
	return (
		<span className={"score " + scoreTone(score)} title="z-score vs baseline">
			{fmtNum(score)}σ
		</span>
	);
}
