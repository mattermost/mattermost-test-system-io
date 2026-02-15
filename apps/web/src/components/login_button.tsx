/**
 * Login/logout button for GitHub OAuth.
 *
 * Shows "Sign in" when unauthenticated and user avatar + logout when authenticated.
 */

import { LogIn, LogOut } from 'lucide-react';
import { useAuth } from '@/contexts/auth_context';
import { useClientConfig } from '@/services/api';

export function LoginButton() {
  const { user, loading, login, logout } = useAuth();
  const { data: config } = useClientConfig();

  // Don't show login button if OAuth is not enabled on the server
  if (!config?.github_oauth_enabled) {
    return null;
  }

  if (loading) {
    return null;
  }

  if (user) {
    return (
      <div className="flex items-center gap-2">
        {user.avatar_url && (
          <img src={user.avatar_url} alt={user.username} className="h-6 w-6 rounded-full" />
        )}
        <span className="hidden text-sm text-gray-700 dark:text-gray-300 sm:inline">
          {user.display_name || user.username}
        </span>
        <button
          type="button"
          onClick={logout}
          className="rounded-md p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-700 dark:text-gray-400 dark:hover:bg-gray-800 dark:hover:text-gray-200"
          title="Sign out"
          aria-label="Sign out"
        >
          <LogOut className="h-4 w-4" />
        </button>
      </div>
    );
  }

  return (
    <button
      type="button"
      onClick={login}
      className="flex items-center gap-1.5 rounded-md px-3 py-1.5 text-sm text-gray-700 transition-colors hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800"
      title="Sign in with GitHub"
      aria-label="Sign in with GitHub"
    >
      <LogIn className="h-4 w-4" />
      <span className="hidden sm:inline">Sign in</span>
    </button>
  );
}
