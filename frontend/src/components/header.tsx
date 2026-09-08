import { ModeToggle } from "@/components/mode-toggle";
import { AccountMenu } from "@/components/account-menu";

export const Header = () => {
  return (
    <header className="w-full flex justify-center">
      <div className="container flex items-center justify-between px-6 py-4">
        <h1 className="font-semibold tracking-tight">Documind</h1>
        <div className="flex items-center gap-3">
          <AccountMenu />
          <ModeToggle />
        </div>
      </div>
    </header>
  );
};
