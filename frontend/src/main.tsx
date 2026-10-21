import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import App from './App';
import { TooltipProvider } from '@/components/ui/tooltip';
import './styles/global.css';
import { registerServiceWorker } from './services/push';

createRoot(document.getElementById('root')!).render(
    <StrictMode>
        <BrowserRouter>
            <TooltipProvider>
                <App />
            </TooltipProvider>
        </BrowserRouter>
    </StrictMode>,
);

registerServiceWorker();
