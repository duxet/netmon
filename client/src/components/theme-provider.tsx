import * as React from "react"

import { ThemeContext, type Theme } from "@/hooks/use-theme"

// Also read by the inline script in index.html to avoid a flash on load.
const STORAGE_KEY = "netmon-theme"

function readStoredTheme(): Theme {
  try {
    const value = localStorage.getItem(STORAGE_KEY)
    if (value === "dark" || value === "light" || value === "system") return value
  } catch {
    // Storage can be unavailable (private mode, blocked site data).
  }
  return "system"
}

const darkQuery = () => window.matchMedia("(prefers-color-scheme: dark)")

function subscribeToSystemTheme(onChange: () => void) {
  const query = darkQuery()
  query.addEventListener("change", onChange)
  return () => query.removeEventListener("change", onChange)
}

export function ThemeProvider({ children }: { children: React.ReactNode }) {
  const [theme, setThemeState] = React.useState<Theme>(readStoredTheme)
  const systemDark = React.useSyncExternalStore(
    subscribeToSystemTheme,
    () => darkQuery().matches,
    () => false
  )
  const resolvedTheme = theme === "system" ? (systemDark ? "dark" : "light") : theme

  React.useEffect(() => {
    document.documentElement.classList.toggle("dark", resolvedTheme === "dark")
  }, [resolvedTheme])

  const setTheme = React.useCallback((next: Theme) => {
    try {
      localStorage.setItem(STORAGE_KEY, next)
    } catch {
      // Ignore: the choice still applies for this visit.
    }
    setThemeState(next)
  }, [])

  const value = React.useMemo(
    () => ({ theme, resolvedTheme, setTheme }),
    [theme, resolvedTheme, setTheme]
  )

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>
}
