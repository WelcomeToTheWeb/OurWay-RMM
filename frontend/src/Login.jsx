import { useState, useEffect, useRef } from "react";
import { useAuth } from "./auth.jsx";
import { Button, Field, Banner } from "./ui/index.js";
import { api } from "./api.js";

export default function Login() {
	const { login, booting, error } = useAuth();
	const [user, setUser] = useState("admin");
	const [pass, setPass] = useState("");
	const [touched, setTouched] = useState(false);
	const [oidcAvailable, setOidcAvailable] = useState(false);
	const passRef = useRef(null);

	useEffect(() => {
		// Move focus to the password field on mount (username defaults to
		// "admin" so the operator only has to type the password).
		passRef.current?.focus();

		// Check if OIDC is available
		api
			.setupStatus()
			.then((status) => {
				if (status && status.available && status.oidc_enabled) {
					setOidcAvailable(true);
				}
			})
			.catch(() => {});
	}, []);

	async function submit(e) {
		e?.preventDefault();
		setTouched(true);
		await login(user, pass);
	}

	async function loginWithOidc() {
		try {
			const response = await api.oidcLogin();
			if (response.auth_url) {
				// Validate URL before redirecting
				const url = new URL(response.auth_url);
				// Only allow http/https schemes
				if (url.protocol !== "http:" && url.protocol !== "https:") {
					throw new Error("Invalid redirect URL");
				}
				// Use replace to avoid leaving the page in browser history
				window.location.replace(url.toString());
			}
		} catch (e) {
			console.error("OIDC login failed:", e);
		}
	}

	return (
		<div className="login-wrap">
			<form className="login card" onSubmit={submit}>
				<header className="login-head">
					<div className="brand">OurWay RMM</div>
					<p className="muted">Sign in to continue</p>
				</header>
				<Field
					label="Username"
					value={user}
					onChange={(e) => setUser(e.target.value)}
					autoComplete="username"
					spellCheck={false}
				/>
				<Field
					label="Password"
					ref={passRef}
					type="password"
					value={pass}
					onChange={(e) => setPass(e.target.value)}
					autoComplete="current-password"
				/>
				{touched && error ? <Banner tone="err">{error}</Banner> : null}
				<Button type="submit" variant="primary" busy={booting}>
					{booting ? "Signing in…" : "Sign in"}
				</Button>
			</form>
			{oidcAvailable && (
				<div className="oidc-login card">
					<Button variant="secondary" onClick={loginWithOidc}>
						Sign in with SSO
					</Button>
				</div>
			)}
		</div>
	);
}
