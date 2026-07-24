import { ModeToggle } from "@/components/mode-toggle";

export const Header = () => {
  return (
    <header className="w-full flex justify-center">
      <div className="container flex justify-around items-center py-4">
        <h1>Documind</h1>
        <ModeToggle />
      </div>
    </header>
  );
};
