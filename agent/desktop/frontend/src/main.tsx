import React from 'react'
import {createRoot} from 'react-dom/client'
import './style.css'
import './App.css'
import App from './App'

const container = document.getElementById('root')

type ErrorBoundaryState = {
    error: Error | null
}

class RootErrorBoundary extends React.Component<{children: React.ReactNode}, ErrorBoundaryState> {
    state: ErrorBoundaryState = {error: null}

    static getDerivedStateFromError(error: Error): ErrorBoundaryState {
        return {error}
    }

    render() {
        if (this.state.error) {
            return (
                <div className="fatal-shell">
                    <section className="fatal-card">
                        <h1>页面渲染失败</h1>
                        <p>客户端捕获到异常，窗口不会再停留在空白蓝色背景。请点击下面按钮恢复界面。</p>
                        <pre>{this.state.error.message || String(this.state.error)}</pre>
                        <button className="primary" onClick={() => this.setState({error: null})}>恢复界面</button>
                    </section>
                </div>
            )
        }
        return this.props.children
    }
}

function showFatal(error: unknown) {
    const message = error instanceof Error ? error.message : String(error)
    if (!container) return
    container.innerHTML = `
        <div class="fatal-shell">
            <section class="fatal-card">
                <h1>页面脚本异常</h1>
                <p>客户端捕获到未处理异常，已阻止空白蓝色背景。</p>
                <pre></pre>
                <button class="primary" type="button">刷新界面</button>
            </section>
        </div>
    `
    const pre = container.querySelector('pre')
    if (pre) pre.textContent = message
    const button = container.querySelector('button')
    if (button) button.addEventListener('click', () => window.location.reload())
}

window.addEventListener('error', (event) => showFatal(event.error || event.message))
window.addEventListener('unhandledrejection', (event) => showFatal(event.reason))

const root = createRoot(container!)

root.render(
    <React.StrictMode>
        <RootErrorBoundary>
            <App/>
        </RootErrorBoundary>
    </React.StrictMode>
)
