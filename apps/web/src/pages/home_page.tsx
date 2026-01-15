import { ReportList } from '../components/report_list';

export function HomePage() {
  return (
    <div>
      <div className="mb-6">
        <h2 className="text-lg font-medium text-gray-900">Reports</h2>
        <p className="mt-1 text-sm text-gray-500">View and manage your test reports</p>
      </div>
      <ReportList />
    </div>
  );
}
