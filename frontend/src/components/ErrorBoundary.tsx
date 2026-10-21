import { Component, type ReactNode, type ErrorInfo } from 'react';

interface Props { children: ReactNode; fallback?: ReactNode }
interface State { hasError: boolean; error: Error | null }

export class ErrorBoundary extends Component<Props, State> {
    state: State = { hasError: false, error: null };

    static getDerivedStateFromError(error: Error) {
        return { hasError: true, error };
    }

    componentDidCatch(error: Error, info: ErrorInfo) {
        console.error('[ErrorBoundary] caught:', error, info.componentStack);
    }

    render() {
        if (this.state.hasError) {
            return this.props.fallback || (
                <div className="p-[24px] m-[16px] rounded-[12px] bg-[rgba(239,68,68,0.1)] border border-solid border-[rgba(239,68,68,0.3)] text-[#ef4444] [font-family:monospace] text-[13px]">
                    <strong>Chart Error:</strong> {this.state.error?.message || 'Unknown error'}
                    <br /><br />
                    <button
                        onClick={() => this.setState({ hasError: false, error: null })}
                        className="py-[6px] px-[16px] bg-[#6366f1] text-white border-none rounded-[6px] cursor-pointer"
                    >
                        Retry
                    </button>
                </div>
            );
        }
        return this.props.children;
    }
}
