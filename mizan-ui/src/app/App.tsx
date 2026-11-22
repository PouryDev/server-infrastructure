import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'
import { AuthProvider, useAuth } from '@/auth/AuthContext'
import { Skeleton, ToastProvider } from '@/ui'
import Login from '@/pages/login/Login'
import { AppShell } from './AppShell'
import { FiscalProvider } from './Fiscal'
import { HIDDEN, ROUTES } from './routes'

function Gate() {
  const { state } = useAuth()
  if (state.status === 'loading') {
    return (
      <div className="app" style={{ alignItems: 'center', justifyContent: 'center' }}>
        <div className="card" style={{ width: 360 }}>
          <Skeleton />
        </div>
      </div>
    )
  }
  if (state.status === 'out') return <Login />
  return (
    <FiscalProvider>
      <Routes>
        <Route element={<AppShell />}>
          {[...ROUTES, ...HIDDEN].map((r) => (
            <Route key={r.key} path={r.path} element={<r.page />} />
          ))}
          <Route path="*" element={<Navigate to="/" replace />} />
        </Route>
      </Routes>
    </FiscalProvider>
  )
}

export function App() {
  return (
    <BrowserRouter>
      <AuthProvider>
        <ToastProvider>
          <Gate />
        </ToastProvider>
      </AuthProvider>
    </BrowserRouter>
  )
}
