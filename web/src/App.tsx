import { Route, Routes } from "react-router-dom";
import { ClusterShell } from "./components/ClusterShell";
import { Layout } from "./components/Layout";
import ClusterViewPage from "./pages/ClusterView";
import ClustersPage from "./pages/Clusters";
import CopilotPage from "./pages/Copilot";
import FleetPage from "./pages/Fleet";
import GuardrailsPage from "./pages/Guardrails";
import HelmReleasesPage from "./pages/HelmReleases";
import InvestigationDetailPage from "./pages/InvestigationDetail";
import CertificatesPage from "./pages/Certificates";
import CostsPage from "./pages/Costs";
import InvestigationsPage from "./pages/Investigations";
import LearningsPage from "./pages/Learnings";
import ReadinessPage from "./pages/Readiness";
import DigestPage from "./pages/Digest";
import IssuesPage from "./pages/Issues";
import LogRulesPage from "./pages/LogRules";
import LoginPage from "./pages/Login";
import MonitorsPage from "./pages/Monitors";
import NodeDetailPage from "./pages/NodeDetail";
import NotFoundPage from "./pages/NotFound";
import PodViewPage from "./pages/PodView";
import ResourceDetailPage from "./pages/ResourceDetail";
import ResourceListPage from "./pages/ResourceList";
import SettingsPage from "./pages/Settings";
import WorkloadsOverviewPage from "./pages/WorkloadsOverview";

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route element={<Layout />}>
        <Route index element={<FleetPage />} />
        <Route path="fleet" element={<FleetPage />} />
        {/* One optional-segment route so the page stays mounted when a new chat gets its id. */}
        <Route path="copilot/:chatId?" element={<CopilotPage />} />
        <Route path="clusters" element={<ClustersPage />} />
        {/* Optional tab segments keep each page mounted while its tab changes. */}
        <Route path="clusters/:id/pods/:ns/:name/:tab?" element={<PodViewPage />} />
        <Route path="clusters/:id" element={<ClusterShell />}>
          <Route path="r/:group/:version/:resource" element={<ResourceListPage />} />
          <Route path="r/:group/:version/:resource/:name" element={<ResourceDetailPage />} />
          <Route path="nodes/:name" element={<NodeDetailPage />} />
          <Route path="helm" element={<HelmReleasesPage />} />
          <Route path="workloads-overview" element={<WorkloadsOverviewPage />} />
          <Route path=":tab?" element={<ClusterViewPage />} />
        </Route>
        <Route path="issues" element={<IssuesPage />} />
        <Route path="certificates" element={<CertificatesPage />} />
        <Route path="costs" element={<CostsPage />} />
        <Route path="investigations" element={<InvestigationsPage />} />
        <Route path="investigations/:id" element={<InvestigationDetailPage />} />
        <Route path="learnings" element={<LearningsPage />} />
        <Route path="readiness" element={<ReadinessPage />} />
        <Route path="digest" element={<DigestPage />} />
        <Route path="monitors" element={<MonitorsPage />} />
        <Route path="log-rules" element={<LogRulesPage />} />
        <Route path="guardrails" element={<GuardrailsPage />} />
        <Route path="settings" element={<SettingsPage />} />
        <Route path="*" element={<NotFoundPage />} />
      </Route>
    </Routes>
  );
}
