import { Link } from "react-router-dom";
import { Page } from "../components/Layout";
import { Empty } from "../components/States";

export default function NotFoundPage() {
  return (
    <Page>
      <div className="card">
        <Empty title="Page not found">
          <Link to="/">Back to Fleet overview</Link>
        </Empty>
      </div>
    </Page>
  );
}
