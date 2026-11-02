import { createContext, useContext } from "react";
import type { Me } from "../api/types";

type MeCtx = { me: Me; setMe: (m: Me) => void };

export const MeContext = createContext<MeCtx | null>(null);

export function useMe(): MeCtx {
  const ctx = useContext(MeContext);
  if (!ctx) throw new Error("useMe must be used inside the authenticated layout");
  return ctx;
}
