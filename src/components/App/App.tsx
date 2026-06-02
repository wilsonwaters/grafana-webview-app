import React from 'react';
import { Route, Routes } from 'react-router-dom';
import { AppRootProps } from '@grafana/data';
import { AppRoot } from './AppRoot';

function App(_props: AppRootProps) {
  return (
    <Routes>
      <Route path="*" element={<AppRoot />} />
    </Routes>
  );
}

export default App;
