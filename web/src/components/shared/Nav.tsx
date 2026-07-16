import { NavLink } from 'react-router-dom';

const links = [
  { to: '/', label: 'Playground' },
  { to: '/schema', label: 'Schema' },
  { to: '/stats', label: 'Statistics' },
];

export function Nav() {
  return (
    <nav className="flex items-center gap-1 px-4 py-2 border-b border-[#2e3347] bg-[#1a1d27] shrink-0">
      <div className="flex items-center gap-2 mr-6">
        <div className="w-6 h-6 bg-indigo-600 rounded flex items-center justify-center text-white text-xs font-bold">Q</div>
        <span className="font-semibold text-sm">QueryEngine</span>
      </div>
      {links.map(({ to, label }) => (
        <NavLink
          key={to}
          to={to}
          end={to === '/'}
          className={({ isActive }) =>
            `px-3 py-1.5 rounded text-sm transition-colors ${
              isActive
                ? 'bg-indigo-600/20 text-indigo-400'
                : 'text-[#8892a4] hover:text-white hover:bg-[#1e2130]'
            }`
          }
        >
          {label}
        </NavLink>
      ))}
    </nav>
  );
}
