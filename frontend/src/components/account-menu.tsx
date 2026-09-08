import { useAuth } from "@/auth/auth-context";
import { Button } from "@/components/ui/button";

export function AccountMenu() {
  const { user, logout } = useAuth();
  return (
    <div className="flex items-center gap-3">
      <span className="hidden max-w-40 truncate text-sm text-muted-foreground sm:block">
        {user?.email || user?.subject}
      </span>
      <Button variant="outline" size="sm" onClick={() => void logout()}>
        Log out
      </Button>
    </div>
  );
}
