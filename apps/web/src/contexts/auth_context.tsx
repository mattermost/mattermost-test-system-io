/**
 * Authentication context for GitHub OAuth.
 *
 * Provides current user state and login/logout actions.
 * Automatically refreshes the access token when it expires.
 */

import { createContext, useContext, useEffect, useState, useCallback, useRef } from 'react';
import type { ReactNode } from 'react';
import type { AuthUser } from '@/types';
import { API_URL } from '@/services/api';

interface AuthContextValue {
  /** Current authenticated user (null if not logged in) */
  user: AuthUser | null;
  /** Whether the auth state is still loading */
  loading: boolean;
  /** Redirect to GitHub OAuth login */
  login: () => void;
  /** Logout and clear session */
  logout: () => Promise<void>;
}

const AuthContext = createContext<AuthContextValue | null>(null);

/** Try to refresh the access token using the refresh token cookie. */
async function tryRefresh(): Promise<boolean> {
  try {
    const response = await fetch(`${API_URL}/auth/refresh`, {
      method: 'POST',
      credentials: 'include',
    });
    return response.ok;
  } catch {
    return false;
  }
}

/** Fetch user info, auto-refreshing if the access token is expired. */
async function fetchUserWithRefresh(): Promise<AuthUser | null> {
  try {
    let response = await fetch(`${API_URL}/auth/me`, {
      credentials: 'include',
    });

    // If access token expired, try refreshing
    if (response.status === 401 || (response.ok && !(await response.clone().json()).user)) {
      // Only try refresh if we got a non-error response with null user
      // (expired access token) — not on hard 401s from other causes
    }

    if (response.ok) {
      const data = await response.json();
      if (data.user) return data.user;
    }

    // Access token might be expired — try refresh
    const refreshed = await tryRefresh();
    if (!refreshed) return null;

    // Retry /auth/me with the new access token
    response = await fetch(`${API_URL}/auth/me`, {
      credentials: 'include',
    });
    if (response.ok) {
      const data = await response.json();
      return data.user || null;
    }

    return null;
  } catch {
    return null;
  }
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<AuthUser | null>(null);
  const [loading, setLoading] = useState(true);
  const refreshTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Fetch current user on mount (with auto-refresh)
  useEffect(() => {
    async function init() {
      const fetchedUser = await fetchUserWithRefresh();
      setUser(fetchedUser);
      setLoading(false);
    }
    init();

    return () => {
      if (refreshTimerRef.current) clearTimeout(refreshTimerRef.current);
    };
  }, []);

  const login = useCallback(() => {
    window.location.href = `${API_URL}/auth/github`;
  }, []);

  const logout = useCallback(async () => {
    try {
      await fetch(`${API_URL}/auth/logout`, {
        method: 'POST',
        credentials: 'include',
      });
    } catch {
      // Ignore errors
    }
    setUser(null);
    if (refreshTimerRef.current) clearTimeout(refreshTimerRef.current);
  }, []);

  return (
    <AuthContext.Provider value={{ user, loading, login, logout }}>{children}</AuthContext.Provider>
  );
}

export function useAuth(): AuthContextValue {
  const context = useContext(AuthContext);
  if (!context) {
    throw new Error('useAuth must be used within an AuthProvider');
  }
  return context;
}
