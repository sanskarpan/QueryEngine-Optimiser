import { BrowserRouter, Routes, Route } from 'react-router-dom';
import { Nav } from './components/shared/Nav';
import { ErrorBoundary } from './components/shared/ErrorBoundary';
import { Playground } from './pages/Playground';
import { Schema } from './pages/Schema';
import { Statistics } from './pages/Statistics';

function App() {
  return (
    <BrowserRouter>
      <div className="flex flex-col h-screen overflow-hidden min-w-[1024px]">
        <Nav />
        <main className="flex-1 min-h-0 overflow-hidden">
          <ErrorBoundary>
            <Routes>
              <Route path="/" element={<Playground />} />
              <Route path="/schema" element={<Schema />} />
              <Route path="/stats" element={<Statistics />} />
            </Routes>
          </ErrorBoundary>
        </main>
      </div>
    </BrowserRouter>
  );
}

export default App;
