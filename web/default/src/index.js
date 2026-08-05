import React, { useEffect, useRef, useState } from 'react';
import ReactDOM from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import { Container } from 'semantic-ui-react';
import App from './App';
import Header from './components/Header';
import Footer from './components/Footer';
import 'semantic-ui-css/semantic.min.css';
import './index.css';
import { UserProvider } from './context/User';
import { ToastContainer } from 'react-toastify';
import 'react-toastify/dist/ReactToastify.css';
import { StatusProvider } from './context/Status';
import './i18n';

// 电脑版是唯一设计基准。其他分辨率只缩放这张画布，不重新排版。
const CANVAS_WIDTH = 1440;

function getCanvasScale() {
  return window.innerWidth / CANVAS_WIDTH;
}

function AppViewport() {
  const canvasRef = useRef(null);
  const [scale, setScale] = useState(getCanvasScale);
  const [canvasHeight, setCanvasHeight] = useState(0);

  useEffect(() => {
    const handleResize = () => setScale(getCanvasScale());
    window.addEventListener('resize', handleResize);
    return () => window.removeEventListener('resize', handleResize);
  }, []);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return undefined;
    const updateHeight = () => setCanvasHeight(canvas.scrollHeight * scale);
    updateHeight();
    const observer = new ResizeObserver(updateHeight);
    observer.observe(canvas);
    return () => observer.disconnect();
  }, [scale]);

  return (
    <div
      className='app-viewport'
      style={canvasHeight ? { height: `${canvasHeight}px` } : undefined}
    >
      <div
        ref={canvasRef}
        className='app-canvas'
        style={{
          width: `${CANVAS_WIDTH}px`,
          transform: `scale(${scale})`,
          transformOrigin: 'top left',
        }}
      >
        <Header />
        <Container className={'main-content'}>
          <App />
        </Container>
        <ToastContainer />
        <Footer />
      </div>
    </div>
  );
}

const root = ReactDOM.createRoot(document.getElementById('root'));
root.render(
  <React.StrictMode>
    <StatusProvider>
      <UserProvider>
        <BrowserRouter>
          <AppViewport />
        </BrowserRouter>
      </UserProvider>
    </StatusProvider>
  </React.StrictMode>
);
