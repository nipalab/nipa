import type { ReactNode } from "react";
import { CloudDownload, FolderOpen } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Logo } from "@/components/Logo";
import { useTranslation } from "react-i18next";

export function Welcome({ adding, onAdd, onClone, leading }: { adding: boolean; onAdd: () => void; onClone: () => void; leading?: ReactNode }) {
  const { t } = useTranslation();
  return (
    <div className="relative flex h-full flex-col items-center justify-center gap-6 p-10 text-center">
      {leading && <div className="absolute left-2 top-2">{leading}</div>}
      <Logo className="size-14" />
      <div className="space-y-2">
        <h1 className="text-xl font-semibold tracking-tight">{t("welcome.welcomeToNipa")}</h1>
        <p className="max-w-sm text-sm text-muted-foreground">
          {t("welcome.cloneAProjectFromYour")}
        </p>
      </div>
      <div className="flex gap-3">
        <Button variant="primary" size="lg" onClick={onClone}>
          <CloudDownload />
          {t("welcome.cloneARepository")}
        </Button>
        <Button size="lg" onClick={onAdd} disabled={adding}>
          <FolderOpen />
          {t("welcome.openAClone")}
        </Button>
      </div>
    </div>
  );
}
