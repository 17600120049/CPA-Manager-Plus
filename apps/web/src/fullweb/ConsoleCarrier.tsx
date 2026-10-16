import symbol from '../assets/brand/cpamp-symbol-color.svg';
import './consoleCarrier.css';

// This lazy module proves the future Console loading seam in the same dist.
export function ConsoleCarrier() {
  const [probe, setProbe] = useState('pending');
  useEffect(() => {
    let active = true;
    createPublicApiClient()
      .get<{ technical: boolean }>('technical-probe')
      .then(
        (result) => {
          if (active) setProbe(result.technical ? 'ok' : 'failed');
        },
        () => {
          if (active) setProbe('unavailable');
        }
      );
    return () => {
      active = false;
    };
  }, []);
  return (
    <p className="technical-carrier">
      <img src={symbol} alt="" />
      Console loading boundary — technical carrier only; API probe: {probe}
    </p>
  );
}
import { useEffect, useState } from 'react';
import { createPublicApiClient } from './publicApi';
